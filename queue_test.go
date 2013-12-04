package main

import (
	"github.com/streadway/amqp"
	"os"
	"reflect"
	"testing"
)

func TestQueue(t *testing.T) {
	url := os.Getenv("AMQP_URL")
	if url == "" {
		url = "amqp://"
	}

	amqpConn, err := amqp.Dial(url)
	if err != nil {
		t.Errorf("Failed to connect to integration AMQP server: %s", err)
		return
	}
	defer amqpConn.Close()

	q, err := NewQueue(amqpConn, "builds.linux", 1)
	if err != nil {
		t.Errorf("Failed to create queue: %s", err)
		return
	}

	want := Payload{
		Job: JobPayload{
			ID:     12345,
			Number: "1",
		},
		Queue: "builds.linux",
	}
	err = q.publish(want)
	if err != nil {
		t.Errorf("Failed to publish payload: %s", err)
		return
	}

	got := <-q.PayloadChannel()
	got.delivery = amqp.Delivery{}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Got payload %+v, wanted %+v", got, want)
	}
}
