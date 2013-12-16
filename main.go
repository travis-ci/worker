package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	var configFile string
	flag.StringVar(&configFile, "configFile", "config/worker.json", "the path to the config file to use")
	flag.Parse()

	logger := NewLogger(os.Stdout, "").Set("pid", os.Getpid())

	logger.Info("loading worker config")

	config, err := ConfigFromFile(configFile)
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

	dispatcher := NewDispatcher(mb, logger)

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
		return NewWorker(mb, dispatcher, logger, config)
	})
}
