package worker

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"

	"github.com/bitly/go-simplejson"
	"github.com/travis-ci/worker/backend"

	gocontext "golang.org/x/net/context"
)

// httpJobQueue is a JobQueue that uses http
type httpJobQueue struct {
	processorPool *ProcessorPool
	jobBoardURL   *url.URL
	queue         string

	DefaultLanguage, DefaultDist, DefaultGroup, DefaultOS string
}

type httpFetchJobsRequest struct {
	Jobs []string `json:"jobs"`
}

type httpFetchJobsResponse struct {
	Jobs []string `json:"jobs"`
}

func NewHTTPJobQueue(pool *ProcessorPool, jobBoardURL *url.URL, queue string) (*httpJobQueue, error) {
	return &httpJobQueue{
		processorPool: pool,
		jobBoardURL:   jobBoardURL,
		queue:         queue,
	}, nil
}

func (q *httpJobQueue) Jobs(ctx gocontext.Context) (outChan <-chan Job, err error) {
	buildJobChan := make(chan Job)
	outChan = buildJobChan

	for {
		jobIds, err := q.fetchJobs()
		for _, id := range jobIds {
			go func(id uint64) {
				buildJob, err := q.fetchJob(id)
				buildJobChan <- buildJob
			}(id)
		}
	}
}

func (q *httpJobQueue) fetchJobs() ([]uint64, error) {
	// POST /jobs?count=17&queue=flah
	// Content-Type: application/json
	// Authorization: Basic ${BASE64_BASIC_AUTH}

	fetchRequestPayload := &httpFetchJobsRequest{}
	numWaiting := 0
	q.processorPool.Each(func(i int, p *Processor) {
		// CurrentStatus is one of "new", "waiting", "processing" or "done"
		switch p.CurrentStatus {
		case "processing":
			fetchRequestPayload.Jobs = append(fetchRequestPayload.Jobs, string(p.LastJobID))
		case "waiting":
			numWaiting++
		}
	})

	jobIdsJSON, err := json.Marshal(fetchRequestPayload)

	// copy jobBoardURL
	url := *q.jobBoardURL

	query := url.Query()
	query.Add("count", string(numWaiting))
	query.Add("queue", q.queue)

	url.Path = "/jobs"
	url.RawQuery = query.Encode()

	client := &http.Client{}

	req, err := http.NewRequest("POST", url.String(), bytes.NewReader(jobIdsJSON))

	// even needed?
	// username := url.User.Username()
	// password, _ := url.User.Password()
	// req.SetBasicAuth(username, password)

	req.Header.Add("Content-Type", "application/json")

	resp, err := client.Do(req)

	fetchResponsePayload := &httpFetchJobsResponse{}
	err = json.NewDecoder(resp.Body).Decode(&fetchResponsePayload)

	var jobIds []uint64
	for _, strID := range fetchResponsePayload.Jobs {
		id, err := strconv.ParseUint(strID, 10, 64)
		jobIds = append(jobIds, id)
	}

	return jobIds, nil
}

func (q *httpJobQueue) fetchJob(id uint64) (Job, error) {
	// GET /jobs/:id
	// Authorization: Basic ${BASE64_BASIC_AUTH}

	buildJob := &httpJob{
		payload:         &JobPayload{},
		startAttributes: &backend.StartAttributes{},
	}
	startAttrs := &jobPayloadStartAttrs{
		Config: &backend.StartAttributes{},
	}

	// copy jobBoardURL
	url := *q.jobBoardURL
	url.Path = "/jobs/" + string(id)

	client := &http.Client{}

	req, err := http.NewRequest("GET", url.String(), nil)

	// even needed?
	// username := url.User.Username()
	// password, _ := url.User.Password()
	// req.SetBasicAuth(username, password)

	req.Header.Add("Content-Type", "application/json")

	resp, err := client.Do(req)

	body, err := ioutil.ReadAll(resp.Body)

	err = json.Unmarshal(body, buildJob.payload)

	err = json.Unmarshal(body, &startAttrs)

	buildJob.rawPayload, err = simplejson.NewJson(body)

	buildJob.startAttributes = startAttrs.Config
	buildJob.startAttributes.VMType = buildJob.payload.VMType
	buildJob.startAttributes.SetDefaults(q.DefaultLanguage, q.DefaultDist, q.DefaultGroup, q.DefaultOS, VMTypeDefault)

	return buildJob, err
}

func (q *httpJobQueue) Cleanup() error {
	return nil
}
