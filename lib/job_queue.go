package lib

import (
	"encoding/json"
	"io"

	"github.com/streadway/amqp"
)

// A JobQueue allows getting Jobs out of an AMQP queue.
type JobQueue struct {
	conn  *amqp.Connection
	queue string
}

type amqpJob struct {
	delivery amqp.Delivery
	payload  JobPayload
}

func (j amqpJob) Payload() JobPayload {
	return j.payload
}

func (j amqpJob) Error(err error) error {
	panic("amqpBuildJob.Error() unimplemented")

	return nil
}

func (j amqpJob) Requeue() error {
	panic("amqpBuildJob.Requeue() unimplemented")

	return nil
}

func (j amqpJob) Finish(state FinishState) error {
	panic("amqpBuildJob.Finish() unimplemented")

	return nil
}

func (j amqpJob) LogWriter() (io.Writer, error) {
	panic("amqpBuildJob.LogWriter() unimplemented")

	return nil, nil
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

	deliveries, err := channel.Consume(q.queue, "build-job-consumer", false, false, false, false, nil)
	if err != nil {
		return
	}

	buildJobChan := make(chan Job)
	outChan = buildJobChan

	go func() {
		for delivery := range deliveries {
			var buildJob amqpJob
			json.Unmarshal(delivery.Body, &buildJob.payload)
			buildJob.delivery = delivery

			buildJobChan <- buildJob
		}

		channel.Close()
	}()

	return
}
