package worker

import (
	"testing"
	"time"

	gocontext "context"

	"github.com/pborman/uuid"
	"github.com/streadway/amqp"
	"github.com/travis-ci/worker/context"
)

func newTestAMQPCanceller(t *testing.T) *AMQPCanceller {
	amqpConn, _ := setupConn(t)

	uuid := uuid.NewRandom()
	ctx := context.FromUUID(gocontext.TODO(), uuid.String())

	return NewAMQPCanceller(ctx, amqpConn)
}

func TestNewAMQPCanceller(t *testing.T) {
	if newTestAMQPCanceller(t) == nil {
		t.Fail()
	}
}

func TestAMQPCanceller_Run(t *testing.T) {
	canceller := newTestAMQPCanceller(t)

	errChan := make(chan interface{})

	go func() {
		defer func() {
			errChan <- recover()
		}()
		canceller.Run()
	}()

	select {
	case <-time.After(3 * time.Second):
	case err := <-errChan:
		if err != nil {
			t.Error(err)
		}
	}
}

func TestAMQPCanceller_Subscribe(t *testing.T) {
	canceller := newTestAMQPCanceller(t)
	jobID := uint64(123)
	subChan := make(chan<- struct{})

	err := canceller.Subscribe(jobID, subChan)
	if err != nil {
		t.Error(err)
	}

	err = canceller.Subscribe(jobID, subChan)
	if err == nil {
		t.Fatalf("no error returned for duplicate subscription")
	}
}

func TestAMQPCanceller_Unsubscribe(t *testing.T) {
	canceller := newTestAMQPCanceller(t)
	jobID := uint64(123)
	subChan := make(chan<- struct{})

	err := canceller.Subscribe(jobID, subChan)
	if err != nil {
		t.Error(err)
	}

	canceller.Unsubscribe(jobID)

	err = canceller.Subscribe(jobID, subChan)
	if err != nil {
		t.Error(err)
	}
}

func TestAMQPCanceller_processCommand(t *testing.T) {
	canceller := newTestAMQPCanceller(t)

	jobID := uint64(123)
	subChan := make(chan<- struct{})

	canceller.Subscribe(jobID, subChan)

	err := canceller.processCommand(amqp.Delivery{Body: []byte("{")})
	if err == nil {
		t.Fatalf("no JSON parsing error returned")
	}

	errChan := make(chan error)

	go func() {
		delivery := amqp.Delivery{Body: []byte(`{
				"type": "cancel_job",
				"job_id": 123,
				"source": "tests"
			}`),
		}
		errChan <- canceller.processCommand(delivery)
	}()

	select {
	case <-time.After(3 * time.Second):
		t.Fatalf("hit potential deadlock condition")
	case err := <-errChan:
		if err != nil {
			t.Error(err)
		}
	}
}
