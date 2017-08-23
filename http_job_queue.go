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
	mismatchedJobIDErr    = fmt.Errorf("returned job ID does not match expected")
)

// HTTPJobQueue is a JobQueue that uses http
type HTTPJobQueue struct {
	jobBoardURL  *url.URL
	site         string
	providerName string
	queue        string
	pollInterval time.Duration
	cb           *CancellationBroadcaster

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
func NewHTTPJobQueue(jobBoardURL *url.URL, site, providerName, queue string,
	cb *CancellationBroadcaster) (*HTTPJobQueue, error) {

	return &HTTPJobQueue{
		jobBoardURL:  jobBoardURL,
		site:         site,
		providerName: providerName,
		queue:        queue,
		pollInterval: time.Second,
		cb:           cb,
	}, nil
}

// Jobs consumes new jobs from job-board
func (q *HTTPJobQueue) Jobs(ctx gocontext.Context) (outChan <-chan Job, err error) {
	buildJobChan := make(chan Job)
	outChan = buildJobChan
	logger := context.LoggerFromContext(ctx).WithFields(logrus.Fields{
		"self": "http_job_queue",
		"inst": fmt.Sprintf("%p", q),
	})

	go func() {
		for {
			logger.Debug("polling for job tick")
			if !q.pollForJob(ctx, buildJobChan) {
				return
			}
			time.Sleep(q.pollInterval)
		}
	}()

	return outChan, nil
}

func (q *HTTPJobQueue) pollForJob(ctx gocontext.Context, buildJobChan chan Job) bool {
	logger := context.LoggerFromContext(ctx).WithFields(logrus.Fields{
		"self": "http_job_queue",
		"inst": fmt.Sprintf("%p", q),
	})

	logger.Debug("fetching job id")
	jobID, err := q.fetchJobID(ctx)
	if err != nil {
		logger.WithField("err", err).Debug("continuing after failing to get job id")
		return true
	}
	logger.WithField("job_id", jobID).Debug("fetching complete job")
	buildJob, err := q.fetchJob(ctx, jobID)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"err": err,
			"id":  jobID,
		}).Warn("failed to get complete job")
		return true
	}

	logger.WithField("job_id", jobID).Debug("sending job to output channel")
	jobSendBegin := time.Now()
	select {
	case buildJobChan <- buildJob:
		metrics.TimeSince("travis.worker.job_queue.http.blocking_time", jobSendBegin)
		logger.WithFields(logrus.Fields{
			"source": "http",
			"dur":    time.Since(jobSendBegin),
		}).Info("sent job to output channel")
		return true
	case <-ctx.Done():
		logger.WithField("err", ctx.Err()).Warn("returning from jobs loop due to context done")
		return false
	}
}

func (q *HTTPJobQueue) fetchJobID(ctx gocontext.Context) (uint64, error) {
	return q.postJobs(ctx, []string{})
}

func (q *HTTPJobQueue) refreshJobClaim(ctx gocontext.Context, jobID uint64) error {
	fetchedID, err := q.postJobs(ctx, []string{strconv.Itoa(int(jobID))})
	if err != nil {
		return err
	}

	if fetchedID != jobID {
		return mismatchedJobIDErr
	}

	return nil
}

func (q *HTTPJobQueue) postJobs(ctx gocontext.Context, jobIDs []string) (uint64, error) {
	logger := context.LoggerFromContext(ctx).WithFields(logrus.Fields{
		"self": "http_job_queue",
		"inst": fmt.Sprintf("%p", q),
	})

	processorID, ok := context.ProcessorFromContext(ctx)
	if !ok {
		processorID = "unknown-processor"
	}
	jobIDsJSON, err := json.Marshal(&httpFetchJobsRequest{Jobs: jobIDs})
	if err != nil {
		return 0, errors.Wrap(err, "failed to marshal job-board jobs request payload")
	}

	u := *q.jobBoardURL

	query := u.Query()
	query.Add("queue", q.queue)

	u.Path = "/jobs"
	u.RawQuery = query.Encode()

	client := &http.Client{}

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader(jobIDsJSON))
	if err != nil {
		return 0, errors.Wrap(err, "failed to create job-board jobs request")
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Travis-Site", q.site)
	req.Header.Add("From", processorID)
	req = req.WithContext(ctx)

	resp, err := client.Do(req)
	if err != nil {
		return 0, errors.Wrap(err, "failed to make job-board jobs request")
	}

	defer resp.Body.Close()
	fetchResponsePayload := &httpFetchJobsResponse{}
	err = json.NewDecoder(resp.Body).Decode(&fetchResponsePayload)
	if err != nil {
		return 0, errors.Wrap(err, "failed to decode job-board jobs response")
	}

	logger.WithField("jobs", fetchResponsePayload.Jobs).Debug("fetched")
	if len(fetchResponsePayload.Jobs) == 0 {
		return 0, httpJobQueueNoJobsErr
	}

	jobID, err := strconv.ParseUint(fetchResponsePayload.Jobs[0], 10, 64)
	if err != nil {
		return 0, errors.Wrap(err, "failed to parse job ID")
	}

	return jobID, nil
}

func (q *HTTPJobQueue) fetchJob(ctx gocontext.Context, jobID uint64) (Job, error) {
	logger := context.LoggerFromContext(ctx).WithFields(logrus.Fields{
		"self": "http_job_queue",
		"inst": fmt.Sprintf("%p", q),
	})

	processorID, ok := context.ProcessorFromContext(ctx)
	if !ok {
		processorID = "unknown-processor"
	}

	buildJob := &httpJob{
		payload: &httpJobPayload{
			Data: &JobPayload{},
		},
		startAttributes: &backend.StartAttributes{},

		refreshClaim: q.generateJobRefreshClaimFunc(jobID),

		jobBoardURL: q.jobBoardURL,
		site:        q.site,
		processorID: processorID,
	}
	startAttrs := &httpJobPayloadStartAttrs{
		Data: &jobPayloadStartAttrs{
			Config: &backend.StartAttributes{},
		},
	}

	u := *q.jobBoardURL
	u.Path = fmt.Sprintf("/jobs/%d", jobID)

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't make job-board job request")
	}

	// TODO: ensure infrastructure is not synonymous with providerName since
	// there's the possibility that a provider has multiple infrastructures, which
	// is expected to be the case with the future cloudbrain provider.
	req.Header.Add("Travis-Infrastructure", q.providerName)
	req.Header.Add("Travis-Site", q.site)
	req.Header.Add("From", processorID)
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
		return nil, errors.Wrap(err, "error making job-board job request")
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "error reading body from job-board job request")
	}

	err = json.Unmarshal(body, buildJob.payload)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal job-board payload")
	}

	err = json.Unmarshal(body, &startAttrs)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal start attributes from job-board")
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

func (q *HTTPJobQueue) generateJobRefreshClaimFunc(jobID uint64) func(gocontext.Context) {
	return func(ctx gocontext.Context) {
		for {
			err := q.refreshJobClaim(ctx, jobID)
			if err == httpJobQueueNoJobsErr {
				context.LoggerFromContext(ctx).WithFields(logrus.Fields{
					"err":    err,
					"job_id": jobID,
				}).Error("failed to refresh claim; cancelling")
				q.cb.Broadcast(jobID)
				return
			}

			if err != nil {
				context.LoggerFromContext(ctx).WithFields(logrus.Fields{
					"err":    err,
					"job_id": jobID,
				}).Error("failed to refresh claim; continuing")
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(q.pollInterval):
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
