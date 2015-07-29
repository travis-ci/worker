package worker

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/streadway/amqp"
	"github.com/travis-ci/worker/context"
	gocontext "golang.org/x/net/context"
)

type cancelCommand struct {
	Type   string `json:"type"`
	JobID  uint64 `json:"job_id"`
	Source string `json:"source"`
}

// AMQPCanceller is responsible for listening to a command queue on AMQP and
// dispatching the commands to the right place. Currently the only valid command
// is the 'cancel job' command.
type AMQPCanceller struct {
	conn *amqp.Connection
	ctx  gocontext.Context

	cancelMutex sync.Mutex
	cancelMap   map[uint64](chan<- struct{})
}

// NewAMQPCanceller creates a new AMQPCanceller. No network traffic
// occurs until you call Run()
func NewAMQPCanceller(ctx gocontext.Context, conn *amqp.Connection) *AMQPCanceller {
	ctx = context.FromComponent(ctx, "command_dispatcher")

	return &AMQPCanceller{
		ctx:       ctx,
		conn:      conn,
		cancelMap: make(map[uint64](chan<- struct{})),
	}
}

// Run will make the AMQPCanceller listen to the worker command queue and
// start dispatching any incoming commands.
func (d *AMQPCanceller) Run() {
	amqpChan, err := d.conn.Channel()
	if err != nil {
		context.LoggerFromContext(d.ctx).WithField("err", err).Error("couldn't open channel")
		return
	}
	defer amqpChan.Close()

	err = amqpChan.Qos(1, 0, false)
	if err != nil {
		context.LoggerFromContext(d.ctx).WithField("err", err).Error("couldn't set prefetch")
		return
	}

	err = amqpChan.ExchangeDeclare("worker.commands", "fanout", false, false, false, false, nil)
	if err != nil {
		context.LoggerFromContext(d.ctx).WithField("err", err).Error("couldn't declare exchange")
		return
	}

	queue, err := amqpChan.QueueDeclare("", true, false, true, false, nil)
	if err != nil {
		context.LoggerFromContext(d.ctx).WithField("err", err).Error("couldn't declare queue")
		return
	}

	err = amqpChan.QueueBind(queue.Name, "", "worker.commands", false, nil)
	if err != nil {
		context.LoggerFromContext(d.ctx).WithField("err", err).Error("couldn't bind queue to exchange")
		return
	}

	deliveries, err := amqpChan.Consume(queue.Name, "commands", false, true, false, false, nil)
	if err != nil {
		context.LoggerFromContext(d.ctx).WithField("err", err).Error("couldn't consume queue")
		return
	}

	for delivery := range deliveries {
		d.processCommand(delivery)
		err := delivery.Ack(false)
		if err != nil {
			context.LoggerFromContext(d.ctx).WithField("err", err).WithField("delivery", delivery).Error("couldn't ack delivery")
		}
	}
}

// Subscribe is an implementation of Canceller.Subscribe.
func (d *AMQPCanceller) Subscribe(id uint64, ch chan<- struct{}) error {
	d.cancelMutex.Lock()
	defer d.cancelMutex.Unlock()

	if _, ok := d.cancelMap[id]; ok {
		return fmt.Errorf("there's already a subscription for job %d", id)
	}

	d.cancelMap[id] = ch

	return nil
}

// Unsubscribe is an implementation of Canceller.Unsubscribe.
func (d *AMQPCanceller) Unsubscribe(id uint64) {
	d.cancelMutex.Lock()
	defer d.cancelMutex.Unlock()

	delete(d.cancelMap, id)
}

func (d *AMQPCanceller) processCommand(delivery amqp.Delivery) {
	var command cancelCommand
	err := json.Unmarshal(delivery.Body, &command)
	if err != nil {
		context.LoggerFromContext(d.ctx).WithField("err", err).Error("unable to parse JSON")
		return
	}

	if command.Type != "cancel_job" {
		context.LoggerFromContext(d.ctx).WithField("command", command.Type).Error("unknown worker command")
		return
	}

	d.cancelMutex.Lock()
	defer d.cancelMutex.Unlock()

	cancelChan, ok := d.cancelMap[command.JobID]
	if !ok {
		context.LoggerFromContext(d.ctx).WithField("command", command.Type).WithField("job", command.JobID).Info("no job with this ID found on this worker")
		return
	}

	if tryClose(cancelChan) {
		context.LoggerFromContext(d.ctx).WithField("command", command.Type).WithField("job", command.JobID).Info("cancelling job")
	} else {
		context.LoggerFromContext(d.ctx).WithField("command", command.Type).WithField("job", command.JobID).Warn("job already cancelled")
	}
}

func tryClose(ch chan<- struct{}) (closedNow bool) {
	closedNow = true
	defer func() {
		if x := recover(); x != nil {
			closedNow = false
		}
	}()

	close(ch)

	return
}
