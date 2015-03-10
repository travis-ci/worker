package lib

import (
	"encoding/json"
	"time"

	"github.com/bitly/go-simplejson"
	"github.com/rcrowley/go-metrics"
	"github.com/streadway/amqp"
	"github.com/travis-ci/worker/lib/backend"
	"github.com/travis-ci/worker/lib/context"
	gocontext "golang.org/x/net/context"
)

// A JobQueue allows getting Jobs out of an AMQP queue.
type JobQueue struct {
	conn  *amqp.Connection
	queue string
}

type amqpJob struct {
	conn            *amqp.Connection
	delivery        amqp.Delivery
	payload         JobPayload
	rawPayload      *simplejson.Json
	startAttributes backend.StartAttributes
}

type amqpPayloadStartAttrs struct {
	Config backend.StartAttributes `json:"config"`
}

func (j amqpJob) Payload() JobPayload {
	return j.payload
}

func (j amqpJob) RawPayload() *simplejson.Json {
	return j.rawPayload
}

func (j amqpJob) StartAttributes() backend.StartAttributes {
	return j.startAttributes
}

func (j amqpJob) Error(ctx gocontext.Context, errMessage string) error {
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

func (j amqpJob) Requeue() error {
	metrics.GetOrRegisterMeter("worker.job.requeue", metrics.DefaultRegistry).Mark(1)

	amqpChan, err := j.conn.Channel()
	if err != nil {
		return err
	}
	defer amqpChan.Close()

	body := map[string]interface{}{
		"id":    j.Payload().Job.ID,
		"state": "reset",
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}

	_, err = amqpChan.QueueDeclare("reporting.jobs.builds", true, false, false, false, nil)
	if err != nil {
		return err
	}

	amqpChan.Publish("", "reporting.jobs.builds", false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now().UTC(),
		Type:         "job:test:reset",
		Body:         bodyBytes,
	})

	j.delivery.Ack(false)

	return nil
}

func (j amqpJob) Received() error {
	amqpChan, err := j.conn.Channel()
	if err != nil {
		return err
	}
	defer amqpChan.Close()

	body := map[string]interface{}{
		"id":          j.Payload().Job.ID,
		"state":       "received",
		"received_at": time.Now().UTC().Format(time.RFC3339),
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}

	_, err = amqpChan.QueueDeclare("reporting.jobs.builds", true, false, false, false, nil)
	if err != nil {
		return err
	}

	return amqpChan.Publish("", "reporting.jobs.builds", false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now().UTC(),
		Type:         "job:test:receive",
		Body:         bodyBytes,
	})
}

func (j amqpJob) Started() error {
	amqpChan, err := j.conn.Channel()
	if err != nil {
		return err
	}
	defer amqpChan.Close()

	body := map[string]interface{}{
		"id":         j.Payload().Job.ID,
		"state":      "started",
		"started_at": time.Now().UTC().Format(time.RFC3339),
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}

	_, err = amqpChan.QueueDeclare("reporting.jobs.builds", true, false, false, false, nil)
	if err != nil {
		return err
	}

	amqpChan.Publish("", "reporting.jobs.builds", false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now().UTC(),
		Type:         "job:test:start",
		Body:         bodyBytes,
	})

	return nil
}

func (j amqpJob) Finish(state FinishState) error {
	amqpChan, err := j.conn.Channel()
	if err != nil {
		return err
	}
	defer amqpChan.Close()

	body := map[string]interface{}{
		"id":          j.Payload().Job.ID,
		"state":       state,
		"finished_at": time.Now().UTC().Format(time.RFC3339),
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}

	_, err = amqpChan.QueueDeclare("reporting.jobs.builds", true, false, false, false, nil)
	if err != nil {
		return err
	}

	amqpChan.Publish("", "reporting.jobs.builds", false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now().UTC(),
		Type:         "job:test:finish",
		Body:         bodyBytes,
	})

	j.delivery.Ack(false)

	return nil
}

func (j amqpJob) LogWriter(ctx gocontext.Context) (LogWriter, error) {
	return NewLogWriter(ctx, j.conn, j.payload.Job.ID)
}

// NewJobQueue creates a JobQueue backed by the given AMQP connections and
// connects to the AMQP queue with the given name. The queue will be declared
// in AMQP when this function is called, so an error could be raised if the
// queue already exists, but with different attributes than we expect.
func NewJobQueue(conn *amqp.Connection, queue string) (*JobQueue, error) {
	channel, err := conn.Channel()
	if err != nil {
		return nil, err
	}

	_, err = channel.QueueDeclare(queue, true, false, false, false, nil)
	if err != nil {
		return nil, err
	}

	err = channel.Close()
	if err != nil {
		return nil, err
	}

	return &JobQueue{
		conn:  conn,
		queue: queue,
	}, nil
}

// Jobs creates a new consumer on the queue, and returns three channels. The
// first channel gets sent every BuildJob that we receive from AMQP. The
// stopChan is a channel that can be closed in order to stop the consumer.
func (q *JobQueue) Jobs(ctx gocontext.Context) (outChan <-chan Job, err error) {
	channel, err := q.conn.Channel()
	if err != nil {
		return
	}

	err = channel.Qos(1, 0, false)
	if err != nil {
		return
	}

	deliveries, err := channel.Consume(q.queue, "build-job-consumer", false, false, false, false, nil)
	if err != nil {
		return
	}

	buildJobChan := make(chan Job)
	outChan = buildJobChan

	go func() {
		for delivery := range deliveries {
			var buildJob amqpJob
			var startAttrs amqpPayloadStartAttrs

			err := json.Unmarshal(delivery.Body, &buildJob.payload)
			if err != nil {
				context.LoggerFromContext(ctx).WithField("err", err).Error("payload JSON parse error")
				delivery.Ack(false)
				continue
			}

			err = json.Unmarshal(delivery.Body, &startAttrs)
			if err != nil {
				context.LoggerFromContext(ctx).WithField("err", err).Error("start attributes JSON parse error")
				delivery.Ack(false)
				continue
			}

			buildJob.rawPayload, err = simplejson.NewJson(delivery.Body)
			if err != nil {
				context.LoggerFromContext(ctx).WithField("err", err).Error("raw payload JSON parse error")
				delivery.Ack(false)
				continue
			}

			buildJob.startAttributes = startAttrs.Config
			buildJob.conn = q.conn
			buildJob.delivery = delivery

			buildJobChan <- buildJob
		}

		channel.Close()
	}()

	return
}
