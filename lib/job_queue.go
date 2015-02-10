package lib

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/streadway/amqp"
	"github.com/travis-ci/worker/lib/backend"
	"golang.org/x/net/context"
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
	startAttributes backend.StartAttributes
}

type amqpPayloadStartAttrs struct {
	Config backend.StartAttributes `json:"config"`
}

func (j amqpJob) Payload() JobPayload {
	return j.payload
}

func (j amqpJob) StartAttributes() backend.StartAttributes {
	return j.startAttributes
}

func (j amqpJob) Error(ctx context.Context, errMessage string) error {
	log, err := j.LogWriter(ctx)
	if err != nil {
		return err
	}

	logWriter, ok := log.(*LogWriter)
	if ok {
		_, err = logWriter.WriteAndClose([]byte(errMessage))
		if err != nil {
			return err
		}
	} else {
		LoggerFromContext(ctx).Error("Log writer wasn't a LogWriter… We're doing Write+Close instead")
		_, err = fmt.Fprintf(log, "%s", errMessage)
		if err != nil {
			return err
		}

		err = log.Close()
		if err != nil {
			return err
		}
	}

	return j.Finish(FinishStateErrored)
}

func (j amqpJob) Requeue() error {
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
		Timestamp:    time.Now(),
		Type:         "job:test:reset",
		Body:         bodyBytes,
	})

	j.delivery.Ack(false)

	return nil
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
		"started_at": time.Now().Format(time.RFC3339),
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
		Timestamp:    time.Now(),
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
		"finished_at": time.Now().Format(time.RFC3339),
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
		Timestamp:    time.Now(),
		Type:         "job:test:finish",
		Body:         bodyBytes,
	})

	j.delivery.Ack(false)

	return nil
}

func (j amqpJob) LogWriter(ctx context.Context) (io.WriteCloser, error) {
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
func (q *JobQueue) Jobs() (outChan <-chan Job, err error) {
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
				fmt.Printf("JSON parse error: %v\n", err)
				continue
			}

			err = json.Unmarshal(delivery.Body, &startAttrs)
			if err != nil {
				fmt.Printf("JSON parse error: %v\n", err)
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
