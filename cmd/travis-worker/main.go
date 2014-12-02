package main

import (
	"os"
	"os/signal"
	"syscall"

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
