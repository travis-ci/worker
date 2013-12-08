package main

import (
	"bytes"
	"testing"
)

type testProcessor struct {
	mb MessageBroker
	t  *testing.T
}

func (p *testProcessor) Process(message []byte) error {
	if !bytes.Equal(message, []byte("hello, world")) {
		p.t.Errorf("wrong message, got = '%s', want 'hello, world'", message)
	}

	p.mb.Close()

	return nil
}

func TestRabbitMessageBroker(t *testing.T) {
	mb, err := NewMessageBroker("amqp://")
	if err != nil {
		t.Errorf("couldn't create message broker: %s", err)
		return
	}

	err = mb.DeclareQueue("test-queue")
	if err != nil {
		t.Errorf("couldn't declare queue")
		return
	}

	err = mb.Publish("", "test-queue", "test", []byte("hello, world"))
	if err != nil {
		t.Errorf("couldn't publish message: %s", err)
		return
	}

	err = mb.Subscribe("test-queue", 1, func() MessageProcessor {
		return &testProcessor{mb, t}
	})
	if err != nil {
		t.Errorf("couldn't subscribe to queue: %s", err)
	}
}
