package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	"github.com/travis-ci/worker/lib"
)

func main() {
	app := cli.NewApp()
	app.Usage = "Travis Worker daemon"
	app.Version = lib.VersionString
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:   "c, config-file",
			Value:  "config/worker.json",
			Usage:  "the path to the config file to use",
			EnvVar: "TRAVIS_WORKER_CONFIG_JSON",
		},
	}
	app.Action = runWorker

	app.Run(os.Args)
}

func runWorker(c *cli.Context) {
	configFile := c.String("config-file")

	logger := logrus.New()
	logger.Info("loading worker config")

	config, err := lib.ConfigFromFile(configFile)
	if err != nil {
		logger.WithField("err", err).Error("error reading config")
		return
	}
	if errs := config.Validate(); len(errs) != 0 {
		logger.Error("config not valid")
		for _, err := range errs {
			logger.WithField("err", err).Error("config error detail")
		}
		return
	}

	logger.Info("connecting to rabbitmq")

	mb, err := lib.NewMessageBroker(config.AMQP.URL)
	if err != nil {
		logger.WithField("err", err).Error("couldn't create a message broker")
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

	logger.WithField("worker_count", config.WorkerCount).Info("starting job processors")

	queue := lib.NewQueue(mb, config.AMQP.Queue, config.WorkerCount)
	queue.Subscribe(gracefulQuit, func() lib.JobPayloadProcessor {
		return lib.NewWorker(mb, dispatcher, metrics, logger, config)
	})
}
