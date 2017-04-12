package worker

import (
	"encoding/json"

	gocontext "context"

	"github.com/bitly/go-simplejson"
	"github.com/streadway/amqp"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
)

// AMQPJobQueue is a JobQueue that uses AMQP
type AMQPJobQueue struct {
	conn  *amqp.Connection
	queue string

	DefaultLanguage, DefaultDist, DefaultGroup, DefaultOS string
}

// NewAMQPJobQueue creates a AMQPJobQueue backed by the given AMQP connections and
// connects to the AMQP queue with the given name. The queue will be declared
// in AMQP when this function is called, so an error could be raised if the
// queue already exists, but with different attributes than we expect.
func NewAMQPJobQueue(conn *amqp.Connection, queue string) (*AMQPJobQueue, error) {
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

	return &AMQPJobQueue{
		conn:  conn,
		queue: queue,
	}, nil
}

// Jobs creates a new consumer on the queue, and returns three channels. The
// first channel gets sent every BuildJob that we receive from AMQP. The
// stopChan is a channel that can be closed in order to stop the consumer.
func (q *AMQPJobQueue) Jobs(ctx gocontext.Context) (outChan <-chan Job, err error) {
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
		defer channel.Close()
		defer close(buildJobChan)

		for {
			if ctx.Err() != nil {
				return
			}

			select {
			case <-ctx.Done():
				return
			case delivery, ok := <-deliveries:
				if !ok {
					context.LoggerFromContext(ctx).Info("job queue channel closed")
					return
				}

				buildJob := &amqpJob{
					payload:         &JobPayload{},
					startAttributes: &backend.StartAttributes{},
				}
				startAttrs := &jobPayloadStartAttrs{Config: &backend.StartAttributes{}}

				err := json.Unmarshal(delivery.Body, buildJob.payload)
				if err != nil {
					context.LoggerFromContext(ctx).WithField("err", err).Error("payload JSON parse error, attempting to nack delivery")
					err := delivery.Ack(false)
					if err != nil {
						context.LoggerFromContext(ctx).WithField("err", err).WithField("delivery", delivery).Error("couldn't nack delivery")
					}
					continue
				}

				context.LoggerFromContext(ctx).WithField("job", buildJob.payload.Job.ID).Info("received amqp delivery")

				err = json.Unmarshal(delivery.Body, &startAttrs)
				if err != nil {
					context.LoggerFromContext(ctx).WithField("err", err).Error("start attributes JSON parse error, attempting to nack delivery")
					err := delivery.Ack(false)
					if err != nil {
						context.LoggerFromContext(ctx).WithField("err", err).WithField("delivery", delivery).Error("couldn't nack delivery")
					}
					continue
				}

				buildJob.rawPayload, err = simplejson.NewJson(delivery.Body)
				if err != nil {
					context.LoggerFromContext(ctx).WithField("err", err).Error("raw payload JSON parse error, attempting to nack delivery")
					err := delivery.Ack(false)
					if err != nil {
						context.LoggerFromContext(ctx).WithField("err", err).WithField("delivery", delivery).Error("couldn't nack delivery")
					}
					continue
				}

				buildJob.startAttributes = startAttrs.Config
				buildJob.startAttributes.VMType = buildJob.payload.VMType
				buildJob.startAttributes.SetDefaults(q.DefaultLanguage, q.DefaultDist, q.DefaultGroup, q.DefaultOS, VMTypeDefault)
				buildJob.conn = q.conn
				buildJob.delivery = delivery
				buildJob.stateCount = buildJob.payload.Meta.StateUpdateCount

				select {
				case buildJobChan <- buildJob:
				case <-ctx.Done():
					delivery.Nack(false, true)
					return
				}
			}
		}
	}()

	return
}

// Cleanup closes the underlying AMQP connection
func (q *AMQPJobQueue) Cleanup() error {
	return q.conn.Close()
}
