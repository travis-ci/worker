package worker

import (
	gocontext "context"
	"time"

	"github.com/streadway/amqp"
)

// AMQPLogsQueue is a LogsQueue that uses AMQP.
type AMQPLogsQueue struct {
	conn            *amqp.Connection
	withLogSharding bool
	logWriterChan   *amqp.Channel
}

// NewAMQPLogsQueue creates a AMQPLogsQueue backed by the given AMQP
// connection and creates the expected exchange and queues.
func NewAMQPLogsQueue(conn *amqp.Connection, sharded bool) (*AMQPLogsQueue, error) {
	channel, err := conn.Channel()
	if err != nil {
		return nil, err
	}

	if sharded {
		// This exchange should be declared as sharded using a policy that matches its name.
		err = channel.ExchangeDeclare("reporting.jobs.logs_sharded", "x-modulus-hash", true, false, false, false, nil)
		if err != nil {
			return nil, err
		}
	} else {
		_, err = channel.QueueDeclare("reporting.jobs.logs", true, false, false, false, nil)
		if err != nil {
			return nil, err
		}

		err = channel.QueueBind("reporting.jobs.logs", "reporting.jobs.logs", "reporting", false, nil)
		if err != nil {
			return nil, err
		}
	}

	return &AMQPLogsQueue{
		conn:            conn,
		withLogSharding: sharded,
		logWriterChan:   channel,
	}, nil
}

func (l *AMQPLogsQueue) LogWriter(ctx gocontext.Context, defaultLogTimeout time.Duration, job Job) (LogWriter, error) {
	logTimeout := time.Duration(job.Payload().Timeouts.LogSilence) * time.Second
	if logTimeout == 0 {
		logTimeout = defaultLogTimeout
	}

	return newAMQPLogWriter(ctx, l.logWriterChan, job.Payload().Job.ID, logTimeout, l.withLogSharding)
}

// Cleanup closes the underlying AMQP connection
func (l *AMQPLogsQueue) Cleanup() error {
	l.logWriterChan.Close()
	return l.conn.Close()
}
