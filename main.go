package main

import (
	"os"
	"os/signal"
	"syscall"
)

func main() {
	logger := NewLogger(os.Stdout, "").Set("pid", os.Getpid())

	logger.Info("loading worker config")

	config, err := ConfigFromFile("config/worker.json")
	if err != nil {
		logger.Errorf("error reading config: %v", err)
		return
	}

	logger = NewLogger(os.Stdout, config.LogTimestamp).Set("pid", os.Getpid())

	logger.Info("connecting to rabbitmq")

	mb, err := NewMessageBroker(config.AMQP.URL)
	if err != nil {
		logger.Errorf("couldn't create a message broker: %v", err)
		return
	}

	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, syscall.SIGTERM, syscall.SIGINT)
	gracefulQuit := make(chan bool)

	go func() {
		<-sigint
		logger.Info("got SIGTERM, shutting down gracefully")
		close(gracefulQuit)
	}()

	logger.Infof("starting %d worker job processors", config.WorkerCount)

	queue := NewQueue(mb, config.AMQP.Queue, config.WorkerCount)
	queue.Subscribe(gracefulQuit, func() JobPayloadProcessor {
		return NewWorker(mb, logger, config)
	})
}
