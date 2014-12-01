package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/travis-ci/worker/lib"
)

func main() {
	var configFile string
	flag.StringVar(&configFile, "configFile", "config/worker.json", "the path to the config file to use")
	flag.Parse()

	logger := lib.NewLogger(os.Stdout, "").Set("pid", os.Getpid())

	logger.Info("loading worker config")

	config, err := lib.ConfigFromFile(configFile)
	if err != nil {
		logger.Errorf("error reading config: %v", err)
		return
	}
	if errs := config.Validate(); len(errs) != 0 {
		logger.Errorf("config not valid")
		for _, err := range errs {
			logger.Error(err.Error())
		}
		return
	}

	logger = lib.NewLogger(os.Stdout, config.LogTimestamp).Set("pid", os.Getpid())

	logger.Info("connecting to rabbitmq")

	mb, err := lib.NewMessageBroker(config.AMQP.URL)
	if err != nil {
		logger.Errorf("couldn't create a message broker: %v", err)
		return
	}

	dispatcher := lib.NewDispatcher(mb, logger)
	metrics := lib.NewMetrics(config.Name, config.Librato)

	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, syscall.SIGTERM, syscall.SIGINT)
	gracefulQuit := make(chan bool)

	go func() {
		<-sigint
		logger.Info("got SIGTERM, shutting down gracefully")
		close(gracefulQuit)
	}()

	logger.Infof("starting %d worker job processors", config.WorkerCount)

	queue := lib.NewQueue(mb, config.AMQP.Queue, config.WorkerCount)
	queue.Subscribe(gracefulQuit, func() lib.JobPayloadProcessor {
		return lib.NewWorker(mb, dispatcher, metrics, logger, config)
	})
}
