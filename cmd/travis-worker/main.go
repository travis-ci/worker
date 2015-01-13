package main

import (
	"fmt"
	"os"
	"os/signal"

	"github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	"github.com/streadway/amqp"
	"github.com/travis-ci/worker/lib"
	"github.com/travis-ci/worker/lib/backend"
	"golang.org/x/net/context"
)

func main() {
	app := cli.NewApp()
	app.Usage = "Travis Worker daemon"
	app.Version = lib.VersionString
	app.Action = runWorker

	app.Run(os.Args)
}

func runWorker(c *cli.Context) {
	logger := logrus.New().WithField("pid", os.Getpid())

	config := lib.EnvToConfig()
	logger.WithField("config", fmt.Sprintf("%+v", config)).Info("read config")

	amqpConn, err := amqp.Dial(config.AmqpURI)
	if err != nil {
		logger.WithField("err", err).Error("couldn't connect to AMQP")
		return
	}

	logger.Info("connected to AMQP")

	generator := lib.NewBuildScriptGenerator(config.BuildAPIURI)
	provider := backend.NewProvider(config.ProviderName, config.ProviderConfig)

	ctx := context.Background()

	pool := &lib.ProcessorPool{
		Context:   ctx,
		Conn:      amqpConn,
		Provider:  provider,
		Generator: generator,
		Logger:    logger,
	}

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)
	go func() {
		<-signalChan
		logger.Info("SIGTERM received, starting graceful shutdown")
		pool.GracefulShutdown()
	}()

	pool.Run(config.PoolSize, "builds.test")

	err = amqpConn.Close()
	if err != nil {
		logger.WithField("err", err).Error("couldn't close AMQP connection")
		return
	}
}
