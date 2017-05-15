package worker

import (
	"os"
	"testing"

	"github.com/streadway/amqp"
)

func setupAMQPConn(t *testing.T) (*amqp.Connection, *amqp.Channel) {
	if os.Getenv("AMQP_URI") == "" {
		t.Skip("skipping amqp test since there is no AMQP_URI")
	}

	amqpConn, err := amqp.Dial(os.Getenv("AMQP_URI"))
	if err != nil {
		t.Fatal(err)
	}

	amqpChan, err := amqpConn.Channel()
	if err != nil {
		t.Fatal(err)
	}

	_, err = amqpChan.QueueDeclare("reporting.jobs.logs", true, false, false, false, nil)
	if err != nil {
		t.Error(err)
	}

	_, err = amqpChan.QueuePurge("reporting.jobs.logs", false)
	if err != nil {
		t.Error(err)
	}

	return amqpConn, amqpChan
}
