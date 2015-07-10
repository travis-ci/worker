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

type Canceller interface {
	Subscribe(id uint64, ch chan<- struct{}) error
	Unsubscribe(id uint64)
}

type CommandDispatcher struct {
	conn *amqp.Connection
	ctx  gocontext.Context

	cancelMutex sync.Mutex
	cancelMap   map[uint64](chan<- struct{})
}

func NewCommandDispatcher(ctx gocontext.Context, conn *amqp.Connection) *CommandDispatcher {
	ctx = context.FromComponent(ctx, "command_dispatcher")

	return &CommandDispatcher{
		ctx:       ctx,
		conn:      conn,
		cancelMap: make(map[uint64](chan<- struct{})),
	}
}

func (d *CommandDispatcher) Run() {
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

func (d *CommandDispatcher) Subscribe(id uint64, ch chan<- struct{}) error {
	d.cancelMutex.Lock()
	defer d.cancelMutex.Unlock()

	if _, ok := d.cancelMap[id]; ok {
		return fmt.Errorf("there's already a subscription for job %d", id)
	}

	d.cancelMap[id] = ch

	return nil
}

func (d *CommandDispatcher) Unsubscribe(id uint64) {
	d.cancelMutex.Lock()
	defer d.cancelMutex.Unlock()

	delete(d.cancelMap, id)
}

func (d *CommandDispatcher) processCommand(delivery amqp.Delivery) {
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
