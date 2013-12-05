package main

import (
	"fmt"
)

func main() {
	config, err := ConfigFromFile("config/worker.json")
	if err != nil {
		fmt.Printf("Error reading config: %v\n", err)
		return
	}

	mb, err := NewMessageBroker(config.AMQP.URL)
	if err != nil {
		fmt.Printf("Couldn't create message broker: %v\n", err)
		return
	}

	queue := NewQueue(mb, config.AMQP.Queue, 10)
	queue.Subscribe(func(jobProcessorNum int) JobPayloadProcessor {
		return NewWorker(fmt.Sprintf("worker-%d", jobProcessorNum), NewBlueBox(config.BlueBox), mb)
	})
}
