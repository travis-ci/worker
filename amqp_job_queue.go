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

// AMQPJobQueue is a JobQueue that uses AMQP
type AMQPJobQueue struct {
	conn  *amqp.Connection
	queue string

	stateUpdatePool *tunny.Pool

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

	_, err = channel.QueueDeclare("reporting.jobs.builds", true, false, false, false, nil)
	if err != nil {
		return nil, err
	}

	err = channel.ExchangeDeclare("reporting", "topic", true, false, false, false, nil)
	if err != nil {
		return nil, err
	}

	_, err = channel.QueueDeclare("reporting.jobs.logs", true, false, false, false, nil)
	if err != nil {
		return nil, err
	}

	err = channel.QueueBind("reporting.jobs.logs", "reporting.jobs.logs", "reporting", false, nil)
	if err != nil {
		return nil, err
	}

	err = channel.Close()
	if err != nil {
		return nil, err
	}

	// TODO: make pool size configurable
	stateUpdatePoolSize := 4
	stateUpdatePool := newStateUpdatePool(conn, stateUpdatePoolSize)

	return &AMQPJobQueue{
		conn:  conn,
		queue: queue,

		stateUpdatePool: stateUpdatePool,
	}, nil
}

func newStateUpdatePool(conn *amqp.Connection, poolSize int) *tunny.Pool {
	return tunny.New(poolSize, func() tunny.Worker {
		stateUpdateChan, err := conn.Channel()
		// TODO: close pool on shutdown
		// defer pool.Close()
		if err != nil {
			// TODO: handle err
			panic(err)
		}
		return &amqpStateUpdateWorker{
			stateUpdateChan: stateUpdateChan,
		}
	})
}

// Jobs creates a new consumer on the queue, and returns three channels. The
// first channel gets sent every BuildJob that we receive from AMQP. The
// stopChan is a channel that can be closed in order to stop the consumer.
func (q *AMQPJobQueue) Jobs(ctx gocontext.Context) (outChan <-chan Job, err error) {
	jobsChannel, err := q.conn.Channel()
	if err != nil {
		return
	}

	err = jobsChannel.Qos(1, 0, false)
	if err != nil {
		return
	}

	deliveries, err := jobsChannel.Consume(q.queue, "build-job-consumer", false, false, false, false, nil)
	if err != nil {
		return
	}

	logWriterChannel, err := q.conn.Channel()
	if err != nil {
		return
	}

	buildJobChan := make(chan Job)
	outChan = buildJobChan

	go func() {
		defer jobsChannel.Close()
		defer logWriterChannel.Close()
		defer close(buildJobChan)

		logger := context.LoggerFromContext(ctx).WithFields(logrus.Fields{
			"self": "amqp_job_queue",
			"inst": fmt.Sprintf("%p", q),
		})

		for {
			if ctx.Err() != nil {
				return
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				continue
			case delivery, ok := <-deliveries:
				if !ok {
					logger.Info("job queue channel closed")
					return
				}

				buildJob := &amqpJob{
					payload:         &JobPayload{},
					startAttributes: &backend.StartAttributes{},
					stateUpdatePool: q.stateUpdatePool,
				}
				startAttrs := &jobPayloadStartAttrs{Config: &backend.StartAttributes{}}

				err := json.Unmarshal(delivery.Body, buildJob.payload)
				if err != nil {
					logger.WithField("err", err).Error("payload JSON parse error, attempting to ack+drop delivery")
					err := delivery.Ack(false)
					if err != nil {
						logger.WithField("err", err).WithField("delivery", delivery).Error("couldn't ack+drop delivery")
					}
					continue
				}

				logger.WithField("job_id", buildJob.payload.Job.ID).Info("received amqp delivery")

				err = json.Unmarshal(delivery.Body, &startAttrs)
				if err != nil {
					logger.WithField("err", err).Error("start attributes JSON parse error, attempting to ack+drop delivery")
					err := delivery.Ack(false)
					if err != nil {
						logger.WithField("err", err).WithField("delivery", delivery).Error("couldn't ack+drop delivery")
					}
					continue
				}

				buildJob.rawPayload, err = simplejson.NewJson(delivery.Body)
				if err != nil {
					logger.WithField("err", err).Error("raw payload JSON parse error, attempting to ack+drop delivery")
					err := delivery.Ack(false)
					if err != nil {
						logger.WithField("err", err).WithField("delivery", delivery).Error("couldn't ack+drop delivery")
					}
					continue
				}

				buildJob.startAttributes = startAttrs.Config
				buildJob.startAttributes.VMType = buildJob.payload.VMType
				buildJob.startAttributes.SetDefaults(q.DefaultLanguage, q.DefaultDist, q.DefaultGroup, q.DefaultOS, VMTypeDefault)
				buildJob.conn = q.conn
				buildJob.logWriterChan = logWriterChannel
				buildJob.delivery = delivery
				buildJob.stateCount = buildJob.payload.Meta.StateUpdateCount

				jobSendBegin := time.Now()
				select {
				case buildJobChan <- buildJob:
					metrics.TimeSince("travis.worker.job_queue.amqp.blocking_time", jobSendBegin)
					logger.WithFields(logrus.Fields{
						"source":           "amqp",
						"send_duration_ms": time.Since(jobSendBegin).Seconds() * 1e3,
					}).Info("sent job to output channel")
				case <-ctx.Done():
					delivery.Nack(false, true)
					return
				}
			}
		}
	}()

	return
}

// Name returns the name of this queue type, wow!
func (q *AMQPJobQueue) Name() string {
	return "amqp"
}

// Cleanup closes the underlying AMQP connection
func (q *AMQPJobQueue) Cleanup() error {
	return q.conn.Close()
}
