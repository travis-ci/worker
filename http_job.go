package worker

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"time"

	gocontext "context"

	"github.com/bitly/go-simplejson"
	"github.com/jtacoma/uritemplates"
	"github.com/pkg/errors"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/metrics"
)

type httpJob struct {
	payload         *httpJobPayload
	rawPayload      *simplejson.Json
	startAttributes *backend.StartAttributes
	received        time.Time
	started         time.Time

	jobBoardURL *url.URL
	site        string
	workerID    string
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
	log, err := j.LogWriter(ctx, time.Minute)
	if err != nil {
		return err
	}

	_, err = log.WriteAndClose([]byte(errMessage))
	if err != nil {
		return err
	}

	return j.Finish(ctx, FinishStateErrored)
}

func (j *httpJob) Requeue(ctx gocontext.Context) error {
	context.LoggerFromContext(ctx).Info("requeueing job")

	metrics.Mark("worker.job.requeue")

	currentState := j.currentState()

	j.received = time.Time{}
	j.started = time.Time{}

	return j.sendStateUpdate(currentState, "created")
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

func (j *httpJob) Finish(ctx gocontext.Context, state FinishState) error {
	context.LoggerFromContext(ctx).WithField("state", state).Info("finishing job")

	u := *j.jobBoardURL
	u.Path = fmt.Sprintf("/jobs/%d", j.Payload().Job.ID)
	u.User = nil

	req, err := http.NewRequest("DELETE", u.String(), nil)
	if err != nil {
		return err
	}

	req.Header.Add("Travis-Site", j.site)
	req.Header.Add("Authorization", "Bearer "+j.payload.JWT)
	req.Header.Add("From", j.workerID)

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusNoContent {
		var errorResp jobBoardErrorResponse
		err := json.Unmarshal(body, &errorResp)
		if err != nil {
			return errors.Wrapf(err, "job board job delete request errored with status %d and didn't send an error response", resp.StatusCode)
		}

		return errors.Errorf("job board job delete request errored with status %d: %s", resp.StatusCode, errorResp.Error)
	}

	finishedAt := time.Now()
	receivedAt := j.received
	if receivedAt.IsZero() {
		receivedAt = finishedAt
	}

	startedAt := j.started
	if startedAt.IsZero() {
		startedAt = finishedAt
	}

	return j.sendStateUpdate(j.currentState(), string(state))
}

func (j *httpJob) LogWriter(ctx gocontext.Context, defaultLogTimeout time.Duration) (LogWriter, error) {
	logTimeout := time.Duration(j.payload.Data.Timeouts.LogSilence) * time.Second
	if logTimeout == 0 {
		logTimeout = defaultLogTimeout
	}

	return newHTTPLogWriter(ctx, j.payload.JobPartsURL, j.payload.JWT, j.payload.Data.Job.ID, logTimeout)
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
