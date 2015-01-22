package main

import (
	"fmt"
	"os"
	"os/signal"

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
	ctx := context.Background()
	logger := lib.LoggerFromContext(ctx)

	config := lib.EnvToConfig()
	logger.WithField("config", fmt.Sprintf("%+v", config)).Debug("read config")

	amqpConn, err := amqp.Dial(config.AmqpURI)
	if err != nil {
		logger.WithField("err", err).Error("couldn't connect to AMQP")
		return
	}

	lib.LoggerFromContext(ctx).Debug("connected to AMQP")

	generator := lib.NewBuildScriptGenerator(config.BuildAPIURI)
	provider, err := backend.NewProvider(config.ProviderName, config.ProviderConfig)
	if err != nil {
		logger.Errorf("couldn't create provider: %v", err)
		return
	}

	pool := &lib.ProcessorPool{
		Context:   ctx,
		Conn:      amqpConn,
		Provider:  provider,
		Generator: generator,
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
		logger.WithField("err", err).Error("couldn't close AMQP connection cleanly")
		return
	}
}
