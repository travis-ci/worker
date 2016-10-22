package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/bitly/go-simplejson"
	"github.com/pkg/errors"
	"github.com/travis-ci/worker/backend"

	gocontext "golang.org/x/net/context"
)

// HTTPJobQueue is a JobQueue that uses http
type HTTPJobQueue struct {
	processorPool *ProcessorPool
	jobBoardURL   *url.URL
	site          string
	queue         string
	workerID      string

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
func NewHTTPJobQueue(pool *ProcessorPool, jobBoardURL *url.URL, site, queue, workerID string) (*HTTPJobQueue, error) {
	return &HTTPJobQueue{
		processorPool: pool,
		jobBoardURL:   jobBoardURL,
		site:          site,
		queue:         queue,
		workerID:      workerID,
	}, nil
}

// Jobs consumes new jobs from job-board
func (q *HTTPJobQueue) Jobs(ctx gocontext.Context) (outChan <-chan Job, err error) {
	buildJobChan := make(chan Job)
	outChan = buildJobChan

	go func() {
		for {
			jobIds, err := q.fetchJobs()
			if err != nil {
				fmt.Fprintf(os.Stderr, "TODO: handle error from httpJobQueue.fetchJobs: %#v", err)
				panic("whoops!")
			}
			for _, id := range jobIds {
				go func(id uint64) {
					buildJob, err := q.fetchJob(id)
					if err != nil {
						fmt.Fprintf(os.Stderr, "TODO: handle error from httpJobQueue.fetchJob: %#v", err)
					}
					buildJobChan <- buildJob
				}(id)
			}

			time.Sleep(time.Second)
		}
	}()

	return outChan, nil
}

func (q *HTTPJobQueue) fetchJobs() ([]uint64, error) {
	// POST /jobs?count=17&queue=flah
	// Content-Type: application/json
	// Travis-Site: ${SITE}
	// Authorization: Basic ${BASE64_BASIC_AUTH}
	// From: ${UNIQUE_ID}

	fetchRequestPayload := &httpFetchJobsRequest{Jobs: []string{}}
	numWaiting := 0
	q.processorPool.Each(func(i int, p *Processor) {
		// CurrentStatus is one of "new", "waiting", "processing" or "done"
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

	// copy jobBoardURL
	url := *q.jobBoardURL

	query := url.Query()
	query.Add("count", strconv.Itoa(numWaiting))
	query.Add("queue", q.queue)

	url.Path = "/jobs"
	url.RawQuery = query.Encode()

	client := &http.Client{}

	req, err := http.NewRequest("POST", url.String(), bytes.NewReader(jobIdsJSON))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create job board jobs request")
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Travis-Site", q.site)
	req.Header.Add("From", q.workerID)

	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to make job board jobs request")
	}

	fetchResponsePayload := &httpFetchJobsResponse{}
	err = json.NewDecoder(resp.Body).Decode(&fetchResponsePayload)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode job board jobs response")
	}

	var jobIds []uint64
	for _, strID := range fetchResponsePayload.Jobs {
		alreadyRunning := false
		for _, prevStrID := range fetchRequestPayload.Jobs {
			if strID == prevStrID {
				alreadyRunning = true
			}
		}
		if alreadyRunning {
			continue
		}

		id, err := strconv.ParseUint(strID, 10, 64)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse job ID")
		}
		jobIds = append(jobIds, id)
	}

	return jobIds, nil
}

func (q *HTTPJobQueue) fetchJob(id uint64) (Job, error) {
	// GET /jobs/:id
	// Authorization: Basic ${BASE64_BASIC_AUTH}
	// Travis-Site: ${SITE}
	// From: ${UNIQUE_ID}

	buildJob := &httpJob{
		payload: &httpJobPayload{
			Data: &JobPayload{},
		},
		startAttributes: &backend.StartAttributes{},
	}
	startAttrs := &httpJobPayloadStartAttrs{
		Data: &jobPayloadStartAttrs{
			Config: &backend.StartAttributes{},
		},
	}

	// copy jobBoardURL
	url := *q.jobBoardURL
	url.Path = fmt.Sprintf("/jobs/%d", id)

	client := &http.Client{}

	req, err := http.NewRequest("GET", url.String(), nil)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't make job board job request")
	}

	req.Header.Add("Travis-Site", q.site)
	req.Header.Add("From", q.workerID)

	resp, err := client.Do(req)
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
			return nil, errors.Errorf("job board job fetch request errored with status %d and didn't send an error response", resp.StatusCode)
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

// Cleanup does not do anything!
func (q *HTTPJobQueue) Cleanup() error {
	return nil
}
