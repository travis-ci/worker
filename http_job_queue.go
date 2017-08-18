package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/bitly/go-simplejson"
	"github.com/cenk/backoff"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/metrics"

	gocontext "context"
)

var (
	httpJobQueueNoJobsErr = fmt.Errorf("no jobs available")
)

type httpPollState uint

const (
	httpPollStateSleep httpPollState = iota
	httpPollStateContinue
	httpPollStateBreak
)

// ProcessorEacherSizer is the minimal interface required by the HTTPJobQueue
// for interacting with the ProcessorPool
type ProcessorEacherSizer interface {
	Each(func(int, *Processor))
	Size() int
}

// HTTPJobQueue is a JobQueue that uses http
type HTTPJobQueue struct {
	processors   ProcessorEacherSizer
	jobBoardURL  *url.URL
	site         string
	providerName string
	queue        string
	workerID     string
	pollInterval time.Duration

	DefaultLanguage, DefaultDist, DefaultGroup, DefaultOS string
}

type httpFetchJobsRequest struct {
	Jobs []string `json:"jobs"`
}

type httpFetchJobsResponse struct {
	Jobs []string `json:"jobs"`
}

type jobBoardErrorResponse struct {
	Type          string `json:"@type"`
	Error         string `json:"error"`
	UpstreamError string `json:"upstream_error,omitempty"`
}

// NewHTTPJobQueue creates a new job-board job queue
func NewHTTPJobQueue(processors ProcessorEacherSizer, jobBoardURL *url.URL, site, providerName, queue, workerID string) (*HTTPJobQueue, error) {
	return &HTTPJobQueue{
		processors:   processors,
		jobBoardURL:  jobBoardURL,
		site:         site,
		providerName: providerName,
		queue:        queue,
		workerID:     workerID,
		// TODO: make pollInterval configurable
		pollInterval: time.Second,
	}, nil
}

// Jobs consumes new jobs from job-board
func (q *HTTPJobQueue) Jobs(ctx gocontext.Context) (outChan <-chan Job, err error) {
	buildJobChan := make(chan Job)
	outChan = buildJobChan

	go func() {
		for {
			switch q.pollForJob(ctx, buildJobChan) {
			case httpPollStateSleep:
				time.Sleep(q.pollInterval)
			case httpPollStateContinue:
				continue
			case httpPollStateBreak:
				return
			}
		}
	}()

	return outChan, nil
}

func (q *HTTPJobQueue) pollForJob(ctx gocontext.Context, buildJobChan chan Job) httpPollState {
	logger := context.LoggerFromContext(ctx).WithField("self", "http_job_queue")
	logger.Debug("fetching job id")
	jobID, err := q.fetchJobID(ctx, 1, []uint64{})
	if err != nil {
		logger.WithField("err", err).Info("continuing after failing to get job id")
		return httpPollStateSleep
	}
	logger.WithField("job_id", jobID).Debug("fetching complete job")
	buildJob, err := q.fetchJob(ctx, jobID)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"err": err,
			"id":  jobID,
		}).Warn("failed to get complete job, sending nil job")
		buildJobChan <- nil
		return httpPollStateSleep
	}
	jobSendBegin := time.Now()
	buildJobChan <- buildJob
	metrics.TimeSince("travis.worker.job_queue.http.blocking_time", jobSendBegin)
	logger.WithFields(logrus.Fields{
		"source": "http",
		"dur":    time.Since(jobSendBegin),
	}).Info("sent job to output channel")

	select {
	case <-ctx.Done():
		logger.WithField("err", ctx.Err()).Warn("returning from jobs loop due to context done")
		return httpPollStateBreak
	default:
	}

	return httpPollStateSleep
}

func (q *HTTPJobQueue) fetchJobID(ctx gocontext.Context, desired uint64, running []uint64) (uint64, error) {
	logger := context.LoggerFromContext(ctx).WithField("self", "http_job_queue")
	processorName, ok := context.ProcessorFromContext(ctx)
	if !ok {
		processorName = "unknown-processor"
	}
	fetchRequestPayload := &httpFetchJobsRequest{Jobs: []string{}}
	for _, jobID := range running {
		fetchRequestPayload.Jobs = append(fetchRequestPayload.Jobs, strconv.Itoa(int(jobID)))
	}

	jobIDsJSON, err := json.Marshal(fetchRequestPayload)
	if err != nil {
		return 0, errors.Wrap(err, "failed to marshal job board jobs request payload")
	}

	u := *q.jobBoardURL

	query := u.Query()
	query.Add("count", strconv.Itoa(int(desired)))
	query.Add("capacity", "1")
	query.Add("queue", q.queue)

	u.Path = "/jobs"
	u.RawQuery = query.Encode()

	client := &http.Client{}

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader(jobIDsJSON))
	if err != nil {
		return 0, errors.Wrap(err, "failed to create job board jobs request")
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Travis-Site", q.site)
	req.Header.Add("From", fmt.Sprintf("%s+%s", processorName, q.workerID))
	req = req.WithContext(ctx)

	resp, err := client.Do(req)
	if err != nil {
		return 0, errors.Wrap(err, "failed to make job board jobs request")
	}

	defer resp.Body.Close()
	fetchResponsePayload := &httpFetchJobsResponse{}
	err = json.NewDecoder(resp.Body).Decode(&fetchResponsePayload)
	if err != nil {
		return 0, errors.Wrap(err, "failed to decode job board jobs response")
	}

	logger.WithField("jobs", fetchResponsePayload.Jobs).Debug("fetched raw jobs")
	var jobIDs []uint64
	for _, strID := range fetchResponsePayload.Jobs {
		alreadyRunning := false
		for _, prevStrID := range fetchRequestPayload.Jobs {
			if strID == prevStrID {
				alreadyRunning = true
			}
		}
		if alreadyRunning {
			logger.WithField("job_id", strID).Debug("skipping running job")
			continue
		}

		id, err := strconv.ParseUint(strID, 10, 64)
		if err != nil {
			return 0, errors.Wrap(err, "failed to parse job ID")
		}
		jobIDs = append(jobIDs, id)
	}

	if len(jobIDs) == 0 {
		return 0, httpJobQueueNoJobsErr
	}

	logger.WithField("job_id", jobIDs[0]).Debug("returning first filtered job ID")
	return jobIDs[0], nil
}

func (q *HTTPJobQueue) fetchJob(ctx gocontext.Context, id uint64) (Job, error) {
	logger := context.LoggerFromContext(ctx).WithField("self", "http_job_queue")
	processorName, ok := context.ProcessorFromContext(ctx)
	if !ok {
		processorName = "unknown-processor"
	}

	buildJob := &httpJob{
		payload: &httpJobPayload{
			Data: &JobPayload{},
		},
		startAttributes: &backend.StartAttributes{},

		refreshClaim: q.generateJobRefreshClaimFunc(10*time.Second, id),

		jobBoardURL: q.jobBoardURL,
		site:        q.site,
		workerID:    fmt.Sprintf("%s+%s", processorName, q.workerID),
	}
	startAttrs := &httpJobPayloadStartAttrs{
		Data: &jobPayloadStartAttrs{
			Config: &backend.StartAttributes{},
		},
	}

	u := *q.jobBoardURL
	u.Path = fmt.Sprintf("/jobs/%d", id)

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't make job board job request")
	}

	// TODO: ensure infrastructure is not synonymous with providerName since
	// there's the possibility that a provider has multiple infrastructures, which
	// is expected to be the case with the future cloudbrain provider.
	req.Header.Add("Travis-Infrastructure", q.providerName)
	req.Header.Add("Travis-Site", q.site)
	req.Header.Add("From", fmt.Sprintf("%s+%s", processorName, q.workerID))
	req = req.WithContext(ctx)

	bo := backoff.NewExponentialBackOff()
	bo.MaxInterval = 10 * time.Second
	bo.MaxElapsedTime = 1 * time.Minute

	var resp *http.Response
	err = backoff.Retry(func() (err error) {
		resp, err = (&http.Client{}).Do(req)
		if resp != nil && resp.StatusCode != http.StatusOK {
			logger.WithFields(logrus.Fields{
				"expected_status": http.StatusOK,
				"actual_status":   resp.StatusCode,
			}).Debug("job fetch failed")

			if resp.Body != nil {
				resp.Body.Close()
			}

			return errors.Errorf("expected %d but got %d", http.StatusOK, resp.StatusCode)
		}
		return
	}, bo)

	if err != nil {
		return nil, errors.Wrap(err, "error making job board job request")
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "error reading body from job board job request")
	}

	err = json.Unmarshal(body, buildJob.payload)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal job board payload")
	}

	err = json.Unmarshal(body, &startAttrs)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal start attributes from job board")
	}

	rawPayload, err := simplejson.NewJson(body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse raw payload with simplejson")
	}
	buildJob.rawPayload = rawPayload.Get("data")

	buildJob.startAttributes = startAttrs.Data.Config
	buildJob.startAttributes.VMType = buildJob.payload.Data.VMType
	buildJob.startAttributes.SetDefaults(q.DefaultLanguage, q.DefaultDist, q.DefaultGroup, q.DefaultOS, VMTypeDefault)

	return buildJob, nil
}

func (q *HTTPJobQueue) generateJobRefreshClaimFunc(pollInterval time.Duration, id uint64) func(gocontext.Context) {
	return func(ctx gocontext.Context) {
		for {
			_, err := q.fetchJobID(ctx, 0, []uint64{id})
			if err != nil && err != httpJobQueueNoJobsErr {
				context.LoggerFromContext(ctx).WithFields(logrus.Fields{
					"err":    err,
					"job_id": id,
				}).Warn("failed to refresh claim")
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
			}
		}
	}
}

// Name returns the name of this queue type, wow!
func (q *HTTPJobQueue) Name() string {
	return "http"
}

// Cleanup does not do anything!
func (q *HTTPJobQueue) Cleanup() error {
	return nil
}
