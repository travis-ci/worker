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
	"github.com/cenk/backoff"
	"github.com/jtacoma/uritemplates"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
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
	finished        time.Time
	stateCount      uint

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
	CurrentState string                  `json:"cur"`
	NewState     string                  `json:"new"`
	Queued       *time.Time              `json:"queued,omitempty"`
	Received     time.Time               `json:"received,omitempty"`
	Started      time.Time               `json:"started,omitempty"`
	Finished     time.Time               `json:"finished,omitempty"`
	Meta         *httpJobStateUpdateMeta `json:"meta,omitempty"`
}

type httpJobStateUpdateMeta struct {
	StateUpdateCount uint `json:"state_update_count,omitempty"`
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
	context.LoggerFromContext(ctx).WithField("self", "http_job").Info("requeueing job")

	metrics.Mark("worker.job.requeue")

	j.received = time.Time{}
	j.started = time.Time{}

	return j.sendStateUpdate(j.currentState(), "created")
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
	logger := context.LoggerFromContext(ctx).WithFields(logrus.Fields{
		"state": state,
		"self":  "http_job",
	})

	logger.Info("finishing job")

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

	bo := backoff.NewExponentialBackOff()
	bo.MaxInterval = 10 * time.Second
	bo.MaxElapsedTime = 1 * time.Minute

	logger.WithField("url", u.String()).Debug("performing DELETE request")

	var resp *http.Response
	err = backoff.Retry(func() (err error) {
		resp, err = (&http.Client{}).Do(req)
		if resp != nil && resp.StatusCode != http.StatusNoContent {
			logger.WithFields(logrus.Fields{
				"expected_status": http.StatusNoContent,
				"actual_status":   resp.StatusCode,
			}).Debug("delete failed")

			return errors.Errorf("expected %d but got %d", http.StatusNoContent, resp.StatusCode)
		}

		return
	}, bo)

	if err != nil {
		return errors.Wrap(err, "failed to mark job complete with retries")
	}

	defer resp.Body.Close()
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

	j.finished = time.Now()
	if j.received.IsZero() {
		j.received = j.finished
	}

	if j.started.IsZero() {
		j.started = j.finished
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

func (j *httpJob) sendStateUpdate(curState, newState string) error {
	j.stateCount++
	payload := &httpJobStateUpdate{
		CurrentState: curState,
		NewState:     newState,
		Queued:       j.Payload().Job.QueuedAt,
		Received:     j.received,
		Started:      j.started,
		Finished:     j.finished,
		Meta: &httpJobStateUpdateMeta{
			StateUpdateCount: j.stateCount,
		},
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
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "error making state update request")
	}

	if resp.StatusCode != http.StatusOK {
		return errors.Errorf("expected %d, but got %d", http.StatusOK, resp.StatusCode)
	}

	return nil
}

func (j *httpJob) Name() string {
	return "http"
}
