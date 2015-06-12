package main

import (
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/Sirupsen/logrus/hooks/sentry"
	"github.com/codegangsta/cli"
	"github.com/rcrowley/go-metrics"
	"github.com/rcrowley/go-metrics/librato"
	"github.com/streadway/amqp"
	"github.com/travis-ci/worker"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
	gocontext "golang.org/x/net/context"
)

func main() {
	app := cli.NewApp()
	app.Usage = "Travis Worker daemon"
	app.Version = worker.VersionString
	app.Author = "Travis CI GmbH"
	app.Email = "contact+travis-worker@travis-ci.org"

	app.Flags = config.Flags
	app.Action = runWorker

	app.Run(os.Args)
}

func runWorker(c *cli.Context) {
	ctx, cancel := gocontext.WithCancel(gocontext.Background())
	logger := context.LoggerFromContext(ctx)

	logrus.SetFormatter(&logrus.TextFormatter{DisableColors: true})

	if c.String("pprof-port") != "" {
		// Start net/http/pprof server
		go func() {
			http.ListenAndServe(fmt.Sprintf("localhost:%s", c.String("pprof-port")), nil)
		}()
	}

	if c.Bool("debug") {
		logrus.SetLevel(logrus.DebugLevel)
	}

	cfg := config.ConfigFromCLIContext(c)

	if c.Bool("echo-config") {
		config.WriteEnvConfig(cfg, os.Stdout)
		return
	}

	logger.WithFields(logrus.Fields{
		"cfg": fmt.Sprintf("%#v", cfg),
	}).Debug("read config")

	logger.Info("worker started")
	defer logger.Info("worker finished")

	if cfg.SentryDSN != "" {
		sentryHook, err := logrus_sentry.NewSentryHook(cfg.SentryDSN, []logrus.Level{logrus.PanicLevel, logrus.FatalLevel, logrus.ErrorLevel})
		if err != nil {
			context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't create sentry hook")
		}

		logrus.AddHook(sentryHook)
	}

	if cfg.LibratoEmail != "" && cfg.LibratoToken != "" && cfg.LibratoSource != "" {
		context.LoggerFromContext(ctx).Info("starting librato metrics reporter")
		go librato.Librato(metrics.DefaultRegistry, time.Minute, cfg.LibratoEmail, cfg.LibratoToken, cfg.LibratoSource, []float64{0.95}, time.Millisecond)
	} else {
		context.LoggerFromContext(ctx).Info("starting logger metrics reporter")
		go metrics.Log(metrics.DefaultRegistry, time.Minute, log.New(os.Stderr, "metrics: ", log.Lmicroseconds))
	}

	amqpConn, err := amqp.Dial(cfg.AmqpURI)
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't connect to AMQP")
		return
	}

	go func() {
		errChan := make(chan *amqp.Error)
		errChan = amqpConn.NotifyClose(errChan)

		err, ok := <-errChan
		if ok {
			context.LoggerFromContext(ctx).WithField("err", err).Error("amqp connection errored, terminating")
			cancel()
		}
	}()

	context.LoggerFromContext(ctx).Debug("connected to AMQP")

	generator := worker.NewBuildScriptGenerator(cfg)
	provider, err := backend.NewProvider(cfg.ProviderName, cfg.ProviderConfig)
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't create backend provider")
		return
	}

	context.LoggerFromContext(ctx).WithFields(logrus.Fields{
		"provider": provider,
	}).Debug("built provider")

	commandDispatcher := worker.NewCommandDispatcher(ctx, amqpConn)
	go commandDispatcher.Run()

	pool := worker.NewProcessorPool(cfg.Hostname, ctx, cfg.HardTimeout, amqpConn,
		provider, generator, commandDispatcher)

	pool.SkipShutdownOnLogTimeout = cfg.SkipShutdownOnLogTimeout

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-signalChan
		if sig == syscall.SIGINT {
			context.LoggerFromContext(ctx).Info("SIGTERM received, starting graceful shutdown")
			pool.GracefulShutdown()
		} else {
			context.LoggerFromContext(ctx).Info("SIGINT received, shutting down immediately")
			cancel()
		}
	}()

	pool.Run(cfg.PoolSize, cfg.QueueName)

	err = amqpConn.Close()
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't close AMQP connection cleanly")
		return
	}
}
