package main

import (
	"os"
)

func main() {
	logger := NewLogger(os.Stdout).Set("pid", os.Getpid())

	logger.Info("About to start the worker")

	config, err := ConfigFromFile("config/worker.json")
	if err != nil {
		logger.Errorf("Error reading config: %v", err)
		return
	}

	mb, err := NewMessageBroker(config.AMQP.URL)
	if err != nil {
		logger.Errorf("Couldn't create message broker: %v", err)
		return
	}

	logger.Infof("Starting %d worker job processors", config.WorkerCount)

	queue := NewQueue(mb, config.AMQP.Queue, config.WorkerCount)
	queue.Subscribe(func() JobPayloadProcessor {
		return NewWorker(config.Name, NewBlueBox(config.BlueBox), mb, logger, config.Timeouts, config.LogLimits)
	})
}
