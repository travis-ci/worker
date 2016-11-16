package worker

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/bitly/go-simplejson"
	"github.com/jtacoma/uritemplates"
	"github.com/pkg/errors"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/metrics"
	gocontext "golang.org/x/net/context"
)

type httpJob struct {
	payload         *httpJobPayload
	rawPayload      *simplejson.Json
	startAttributes *backend.StartAttributes
	received        time.Time
	started         time.Time
}

type jobScriptPayload struct {
	Name     string `json:"name"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

type httpJobPayload struct {
	Data        *JobPayload      `json:"data"`
	JobScript   jobScriptPayload `json:"job_script"`
	JobStateURL string           `json:"job_state_url"`
	JobPartsURL string           `json:"log_parts_url"`
	JWT         string           `json:"jwt"`
	ImageName   string           `json:"image_name"`
}

type httpJobStateUpdate struct {
	CurrentState string    `json:"cur"`
	NewState     string    `json:"new"`
	ReceivedAt   time.Time `json:"received,omitempty"`
	StartedAt    time.Time `json:"started,omitempty"`
}

func (j *httpJob) GoString() string {
	return fmt.Sprintf("&httpJob{payload: %#v, startAttributes: %#v}",
		j.payload, j.startAttributes)
}

func (j *httpJob) Payload() *JobPayload {
	return j.payload.Data
}

func (j *httpJob) RawPayload() *simplejson.Json {
	return j.rawPayload
}

func (j *httpJob) StartAttributes() *backend.StartAttributes {
	return j.startAttributes
}

func (j *httpJob) Error(ctx gocontext.Context, errMessage string) error {
	log, err := j.LogWriter(ctx)
	if err != nil {
		return err
	}

	_, err = log.WriteAndClose([]byte(errMessage))
	if err != nil {
		return err
	}

	return j.Finish(FinishStateErrored)
}

func (j *httpJob) Requeue() error {
	metrics.Mark("worker.job.requeue")

	currentState := j.currentState()

	j.received = time.Time{}
	j.started = time.Time{}

	err := j.sendStateUpdate(currentState, "created")

	if err != nil {
		return err
	}

	return nil
}

func (j *httpJob) Received() error {
	j.received = time.Now()
	return j.sendStateUpdate("queued", "received")
}

func (j *httpJob) Started() error {
	j.started = time.Now()

	metrics.TimeSince("travis.worker.job.start_time", j.received)

	return j.sendStateUpdate("received", "started")
}

func (j *httpJob) currentState() string {

	currentState := "queued"

	if !j.received.IsZero() {
		currentState = "received"
	}

	if !j.started.IsZero() {
		currentState = "started"
	}

	return currentState
}

func (j *httpJob) Finish(state FinishState) error {
	// DELETE /jobs/:id
	// Authorization: Bearer ${JWT}
	// Travis-Site: ${SITE}
	// From: ${UNIQUE_ID}

	// copy jobBoardURL
	url := *q.jobBoardURL
	url.Path = "/jobs/" + string(id)
	url.Userinfo = nil

	client := &http.Client{}

	req, err := http.NewRequest("DELETE", url.String(), nil)

	req.Header.Add("Travis-Site", q.site)
	req.Header.Add("Authorization", "Bearer "+j.Payload().JWT)
	req.Header.Add("From", q.uniqueID)

	resp, err := client.Do(req)

	currentState := j.currentState()

	finishedAt := time.Now()
	receivedAt := j.received
	if receivedAt.IsZero() {
		receivedAt = finishedAt
	}
	startedAt := j.started
	if startedAt.IsZero() {
		startedAt = finishedAt
	}

	err := j.sendStateUpdate(currentState, string(state))
	if err != nil {
		return err
	}

	return nil
}

func (j *httpJob) LogWriter(ctx gocontext.Context) (LogWriter, error) {
	return newHTTPLogWriter(ctx, j.payload.JobPartsURL, j.payload.JWT, j.payload.Data.Job.ID)
}

func (j *httpJob) Generate(ctx gocontext.Context, job Job) ([]byte, error) {
	if j.payload.JobScript.Encoding != "base64" {
		return nil, errors.Errorf("unknown job script encoding: %s", j.payload.JobScript.Encoding)
	}

	script, err := base64.StdEncoding.DecodeString(j.payload.JobScript.Content)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't base64 decode job script")
	}

	return script, nil
}

func (j *httpJob) sendStateUpdate(currentState, newState string) error {
	payload := &httpJobStateUpdate{
		CurrentState: currentState,
		NewState:     newState,
		ReceivedAt:   j.received,
		StartedAt:    j.started,
	}

	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return errors.Wrap(err, "error encoding json")
	}

	template, err := uritemplates.Parse(j.payload.JobStateURL)
	if err != nil {
		return errors.Wrap(err, "couldn't parse base URL template")
	}

	u, err := template.Expand(map[string]interface{}{
		"job_id": j.payload.Data.Job.ID,
	})
	if err != nil {
		return errors.Wrap(err, "couldn't expand base URL template")
	}

	req, err := http.NewRequest("PATCH", u, bytes.NewReader(encodedPayload))
	if err != nil {
		return errors.Wrap(err, "couldn't create request")
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", j.payload.JWT))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "error making state update request")
	}

	if resp.StatusCode != http.StatusOK {
		return errors.Errorf("expected %d, but got %d", http.StatusOK, resp.StatusCode)
	}

	return nil
}
