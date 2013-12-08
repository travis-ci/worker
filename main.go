package main

import (
	"fmt"
	"os"
)

func main() {
	logger := NewLogger(os.Stdout).Set("pid", os.Getpid())

	config, err := ConfigFromFile("config/worker.json")
	if err != nil {
		logger.Errorf("error reading config: %v", err)
		return
	}

	mb, err := NewMessageBroker(config.AMQP.URL)
	if err != nil {
		logger.Errorf("couldn't create message broker: %v", err)
		return
	}

	queue := NewQueue(mb, config.AMQP.Queue, config.WorkerCount)
	queue.Subscribe(func(jobProcessorNum int) JobPayloadProcessor {
		return NewWorker(fmt.Sprintf("worker-%d", jobProcessorNum), NewBlueBox(config.BlueBox), mb, logger, config.Timeouts, config.LogLimits)
	})
}
