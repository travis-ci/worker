package worker

import (
	"encoding/json"
	"fmt"
	"time"

	gocontext "context"

	"github.com/Jeffail/tunny"
	"github.com/bitly/go-simplejson"
	"github.com/sirupsen/logrus"
	"github.com/streadway/amqp"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/metrics"
)

type amqpJob struct {
	conn            *amqp.Connection
	stateUpdatePool *tunny.Pool
	logWriterChan   *amqp.Channel
	delivery        amqp.Delivery
	payload         *JobPayload
	rawPayload      *simplejson.Json
	startAttributes *backend.StartAttributes
	received        time.Time
	started         time.Time
	finished        time.Time
	stateCount      uint
}

func (j *amqpJob) GoString() string {
	return fmt.Sprintf("&amqpJob{conn: %#v, delivery: %#v, payload: %#v, startAttributes: %#v}",
		j.conn, j.delivery, j.payload, j.startAttributes)
}

func (j *amqpJob) Payload() *JobPayload {
	return j.payload
}

func (j *amqpJob) RawPayload() *simplejson.Json {
	return j.rawPayload
}

func (j *amqpJob) StartAttributes() *backend.StartAttributes {
	return j.startAttributes
}

func (j *amqpJob) Error(ctx gocontext.Context, errMessage string) error {
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

func (j *amqpJob) Requeue(ctx gocontext.Context) error {
	context.LoggerFromContext(ctx).WithFields(
		logrus.Fields{
			"self":       "amqp_job",
			"job_id":     j.Payload().Job.ID,
			"repository": j.Payload().Repository.Slug,
		}).Info("requeueing job")

	metrics.Mark("worker.job.requeue")

	err := j.sendStateUpdate(ctx, "job:test:reset", "reset")
	if err != nil {
		return err
	}

	return j.delivery.Ack(false)
}

func (j *amqpJob) Received(ctx gocontext.Context) error {
	j.received = time.Now()

	if j.payload.Job.QueuedAt != nil {
		metrics.TimeSince("travis.worker.job.queue_time", *j.payload.Job.QueuedAt)
	}

	return j.sendStateUpdate(ctx, "job:test:receive", "received")
}

func (j *amqpJob) Started(ctx gocontext.Context) error {
	j.started = time.Now()

	metrics.TimeSince("travis.worker.job.start_time", j.received)

	return j.sendStateUpdate(ctx, "job:test:start", "started")
}

func (j *amqpJob) Finish(ctx gocontext.Context, state FinishState) error {
	context.LoggerFromContext(ctx).WithFields(logrus.Fields{
		"state":      state,
		"self":       "amqp_job",
		"job_id":     j.Payload().Job.ID,
		"repository": j.Payload().Repository.Slug,
	}).Info("finishing job")

	j.finished = time.Now()
	if j.received.IsZero() {
		j.received = j.finished
	}

	if j.started.IsZero() {
		j.started = j.finished
	}

	metrics.Mark(fmt.Sprintf("travis.worker.job.finish.%s", state))
	metrics.Mark("travis.worker.job.finish")

	err := j.sendStateUpdate(ctx, "job:test:finish", string(state))
	if err != nil {
		return err
	}

	return j.delivery.Ack(false)
}

func (j *amqpJob) LogWriter(ctx gocontext.Context, defaultLogTimeout time.Duration) (LogWriter, error) {
	logTimeout := time.Duration(j.payload.Timeouts.LogSilence) * time.Second
	if logTimeout == 0 {
		logTimeout = defaultLogTimeout
	}

	return newAMQPLogWriter(ctx, j.logWriterChan, j.payload.Job.ID, logTimeout)
}

func (j *amqpJob) createStateUpdateBody(ctx gocontext.Context, state string) map[string]interface{} {
	body := map[string]interface{}{
		"id":    j.Payload().Job.ID,
		"state": state,
		"meta": map[string]interface{}{
			"state_update_count": j.stateCount,
		},
	}

	if instanceID, ok := context.InstanceIDFromContext(ctx); ok {
		body["meta"].(map[string]interface{})["instance_id"] = instanceID
	}

	if j.Payload().Job.QueuedAt != nil {
		body["queued_at"] = j.Payload().Job.QueuedAt.UTC().Format(time.RFC3339)
	}
	if !j.received.IsZero() {
		body["received_at"] = j.received.UTC().Format(time.RFC3339)
	}
	if !j.started.IsZero() {
		body["started_at"] = j.started.UTC().Format(time.RFC3339)
	}
	if !j.finished.IsZero() {
		body["finished_at"] = j.finished.UTC().Format(time.RFC3339)
	}

	return body
}

func (j *amqpJob) sendStateUpdate(ctx gocontext.Context, event, state string) error {
	err := j.stateUpdatePool.Process(&amqpStateUpdatePayload{
		job:   j,
		ctx:   ctx,
		event: event,
		state: state,
		body:  j.createStateUpdateBody(ctx, state),
	})

	if err == nil {
		return nil
	}

	return err.(error)
}

func (j *amqpJob) SetupContext(ctx gocontext.Context) gocontext.Context { return ctx }

func (j *amqpJob) Name() string { return "amqp" }

type amqpStateUpdatePayload struct {
	job   *amqpJob
	ctx   gocontext.Context
	event string
	state string
	body  map[string]interface{}
}

type amqpStateUpdateWorker struct {
	stateUpdateChan *amqp.Channel
	ctx             gocontext.Context
	cancel          gocontext.CancelFunc
}

func (w *amqpStateUpdateWorker) Process(payload interface{}) interface{} {
	p := payload.(*amqpStateUpdatePayload)
	ctx, cancel := gocontext.WithCancel(p.ctx)

	w.ctx = ctx
	w.cancel = cancel

	return w.sendStateUpdate(p)
}

func (w *amqpStateUpdateWorker) BlockUntilReady() {
	// we do not need to perform any warm-up before processing jobs.
	// Process() will block for the duration of the job itself.
}

func (w *amqpStateUpdateWorker) Interrupt() {
	w.cancel()
}

func (w *amqpStateUpdateWorker) Terminate() {
	err := w.stateUpdateChan.Close()
	if err != nil {
		time.Sleep(time.Minute)
		logrus.WithFields(logrus.Fields{
			"self": "amqp_state_update_worker",
			"err":  err,
		}).Panic("timed out waiting for shutdown after amqp connection error")
	}
}

func (w *amqpStateUpdateWorker) sendStateUpdate(payload *amqpStateUpdatePayload) error {
	select {
	case <-w.ctx.Done():
		return w.ctx.Err()
	default:
	}

	payload.job.stateCount++

	bodyBytes, err := json.Marshal(payload.body)
	if err != nil {
		return err
	}

	return w.stateUpdateChan.Publish("", "reporting.jobs.builds", false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now().UTC(),
		Type:         payload.event,
		Body:         bodyBytes,
	})
}
