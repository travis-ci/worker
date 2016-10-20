package worker

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/bitly/go-simplejson"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/metrics"
	gocontext "golang.org/x/net/context"
)

type httpJob struct {
	payload         *JobPayload
	rawPayload      *simplejson.Json
	startAttributes *backend.StartAttributes
	received        time.Time
	started         time.Time
}

func (j *httpJob) GoString() string {
	return fmt.Sprintf("&httpJob{conn: %#v, delivery: %#v, payload: %#v, startAttributes: %#v}",
		j.conn, j.delivery, j.payload, j.startAttributes)
}

func (j *httpJob) Payload() *JobPayload {
	return j.payload
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

	err := j.sendStateUpdate("job:test:reset", map[string]interface{}{
		"id":    j.Payload().Job.ID,
		"state": "reset",
	})
	if err != nil {
		return err
	}

	return j.delivery.Ack(false)
}

func (j *httpJob) Received() error {
	j.received = time.Now()
	return j.sendStateUpdate("job:test:receive", map[string]interface{}{
		"id":          j.Payload().Job.ID,
		"state":       "received",
		"received_at": j.received.UTC().Format(time.RFC3339),
	})
}

func (j *httpJob) Started() error {
	j.started = time.Now()

	metrics.TimeSince("travis.worker.job.start_time", j.received)

	return j.sendStateUpdate("job:test:start", map[string]interface{}{
		"id":          j.Payload().Job.ID,
		"state":       "started",
		"received_at": j.received.UTC().Format(time.RFC3339),
		"started_at":  j.started.UTC().Format(time.RFC3339),
	})
}

func (j *httpJob) Finish(state FinishState) error {
	finishedAt := time.Now()
	receivedAt := j.received
	if receivedAt.IsZero() {
		receivedAt = finishedAt
	}
	startedAt := j.started
	if startedAt.IsZero() {
		startedAt = finishedAt
	}

	err := j.sendStateUpdate("job:test:finish", map[string]interface{}{
		"id":          j.Payload().Job.ID,
		"state":       state,
		"received_at": receivedAt.UTC().Format(time.RFC3339),
		"started_at":  startedAt.UTC().Format(time.RFC3339),
		"finished_at": finishedAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}

	return j.delivery.Ack(false)
}

func (j *httpJob) LogWriter(ctx gocontext.Context) (LogWriter, error) {
	return newhttpLogWriter(ctx, j.conn, j.payload.Job.ID)
}

func (j *httpJob) sendStateUpdate(event string, body map[string]interface{}) error {
	httpChan, err := j.conn.Channel()
	if err != nil {
		return err
	}
	defer httpChan.Close()

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}

	_, err = httpChan.QueueDeclare("reporting.jobs.builds", true, false, false, false, nil)
	if err != nil {
		return err
	}

	return httpChan.Publish("", "reporting.jobs.builds", false, false, http.Publishing{
		ContentType:  "application/json",
		DeliveryMode: http.Persistent,
		Timestamp:    time.Now().UTC(),
		Type:         event,
		Body:         bodyBytes,
	})
}
