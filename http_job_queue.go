package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/bitly/go-simplejson"
	"github.com/pkg/errors"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"

	gocontext "context"
)

// HTTPJobQueue is a JobQueue that uses http
type HTTPJobQueue struct {
	processorPool     *ProcessorPool
	jobBoardURL       *url.URL
	site              string
	providerName      string
	queue             string
	workerID          string
	buildJobChan      chan Job
	buildJobChanMutex *sync.Mutex

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
func NewHTTPJobQueue(pool *ProcessorPool, jobBoardURL *url.URL, site, providerName, queue, workerID string) (*HTTPJobQueue, error) {
	return &HTTPJobQueue{
		processorPool:     pool,
		jobBoardURL:       jobBoardURL,
		site:              site,
		providerName:      providerName,
		queue:             queue,
		workerID:          workerID,
		buildJobChanMutex: &sync.Mutex{},
	}, nil
}

// Jobs consumes new jobs from job-board
func (q *HTTPJobQueue) Jobs(ctx gocontext.Context) (outChan <-chan Job, err error) {
	q.buildJobChanMutex.Lock()
	defer q.buildJobChanMutex.Unlock()
	logger := context.LoggerFromContext(ctx).WithField("self", "http_job_queue")
	if q.buildJobChan != nil {
		return q.buildJobChan, nil
	}

	buildJobChan := make(chan Job)
	outChan = buildJobChan

	go func() {
		for {
			logger.Debug("fetching job ids")
			jobIds, err := q.fetchJobs(ctx)
			if err != nil {
				logger.WithField("err", err).Warn("continuing after failing to get job ids")
				time.Sleep(time.Second)
				continue
			}
			for _, id := range jobIds {
				go func(id uint64) {
					logger.WithField("job_id", id).Debug("fetching complete job")
					buildJob, err := q.fetchJob(ctx, id)
					if err != nil {
						logger.WithField("err", err).Warn("breaking after failing to get complete job")
						return
					}
					logger.WithField("job", buildJob).Debug("sending job to output channel")
					buildJobChan <- buildJob
				}(id)
			}

			select {
			case <-time.After(time.Second):
				logger.Debug("jobs loop again after 1s sleep")
				continue
			case <-ctx.Done():
				logger.WithField("err", ctx.Err()).Warn("returning from jobs loop due to context done")
				q.buildJobChan = nil
				return
			}
		}
	}()

	q.buildJobChan = buildJobChan
	return outChan, nil
}

func (q *HTTPJobQueue) fetchJobs(ctx gocontext.Context) ([]uint64, error) {
	logger := context.LoggerFromContext(ctx).WithField("self", "http_job_queue")
	fetchRequestPayload := &httpFetchJobsRequest{Jobs: []string{}}
	numWaiting := 0
	q.processorPool.Each(func(i int, p *Processor) {
		switch p.CurrentStatus {
		case "processing":
			fetchRequestPayload.Jobs = append(fetchRequestPayload.Jobs, strconv.FormatUint(p.LastJobID, 10))
		case "waiting", "new":
			numWaiting++
		}
	})

	jobIdsJSON, err := json.Marshal(fetchRequestPayload)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal job board jobs request payload")
	}

	u := *q.jobBoardURL

	query := u.Query()
	query.Add("count", strconv.Itoa(numWaiting))
	query.Add("queue", q.queue)

	u.Path = "/jobs"
	u.RawQuery = query.Encode()

	client := &http.Client{}

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader(jobIdsJSON))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create job board jobs request")
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Travis-Site", q.site)
	req.Header.Add("From", q.workerID)
	req = req.WithContext(ctx)

	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to make job board jobs request")
	}

	fetchResponsePayload := &httpFetchJobsResponse{}
	err = json.NewDecoder(resp.Body).Decode(&fetchResponsePayload)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode job board jobs response")
	}

	logger.WithField("jobs", fetchResponsePayload.Jobs).Debug("fetched raw jobs")
	var jobIds []uint64
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
			return nil, errors.Wrap(err, "failed to parse job ID")
		}
		jobIds = append(jobIds, id)
	}

	logger.WithField("jobs", jobIds).Debug("returning filtered job IDs")
	return jobIds, nil
}

func (q *HTTPJobQueue) fetchJob(ctx gocontext.Context, id uint64) (Job, error) {
	buildJob := &httpJob{
		payload: &httpJobPayload{
			Data: &JobPayload{},
		},
		startAttributes: &backend.StartAttributes{},

		jobBoardURL: q.jobBoardURL,
		site:        q.site,
		workerID:    q.workerID,
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
	req.Header.Add("From", q.workerID)
	req = req.WithContext(ctx)

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "error making job board job request")
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "error reading body from job board job request")
	}

	if resp.StatusCode != http.StatusOK {
		var errorResp jobBoardErrorResponse
		err := json.Unmarshal(body, &errorResp)
		if err != nil {
			return nil, errors.Wrapf(err, "job board job fetch request errored with status %d and didn't send an error response", resp.StatusCode)
		}

		return nil, errors.Errorf("job board job fetch request errored with status %d: %s", resp.StatusCode, errorResp.Error)
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

// Name returns the name of this queue type, wow!
func (q *HTTPJobQueue) Name() string {
	return "http"
}

// Cleanup does not do anything!
func (q *HTTPJobQueue) Cleanup() error {
	return nil
}
