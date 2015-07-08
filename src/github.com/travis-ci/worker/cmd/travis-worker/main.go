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

var (
	bootTime = time.Now().UTC()
)

func main() {
	app := cli.NewApp()
	app.Usage = "Travis Worker daemon"
	app.Version = worker.VersionString
	app.Author = "Travis CI GmbH"
	app.Email = "contact+travis-worker@travis-ci.com"

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
			logger.WithField("err", err).Error("couldn't create sentry hook")
		}

		logrus.AddHook(sentryHook)
	}

	if cfg.LibratoEmail != "" && cfg.LibratoToken != "" && cfg.LibratoSource != "" {
		logger.Info("starting librato metrics reporter")
		go librato.Librato(metrics.DefaultRegistry, time.Minute, cfg.LibratoEmail, cfg.LibratoToken, cfg.LibratoSource, []float64{0.95}, time.Millisecond)
	} else if !c.Bool("silence-metrics") {
		logger.Info("starting logger metrics reporter")
		go metrics.Log(metrics.DefaultRegistry, time.Minute, log.New(os.Stderr, "metrics: ", log.Lmicroseconds))
	}

	amqpConn, err := amqp.Dial(cfg.AmqpURI)
	if err != nil {
		logger.WithField("err", err).Error("couldn't connect to AMQP")
		return
	}

	go func() {
		errChan := make(chan *amqp.Error)
		errChan = amqpConn.NotifyClose(errChan)

		err, ok := <-errChan
		if ok {
			logger.WithField("err", err).Error("amqp connection errored, terminating")
			cancel()
		}
	}()

	logger.Debug("connected to AMQP")

	generator := worker.NewBuildScriptGenerator(cfg)
	logger.WithFields(logrus.Fields{
		"build_script_generator": fmt.Sprintf("%#v", generator),
	}).Debug("built")

	provider, err := backend.NewProvider(cfg.ProviderName, cfg.ProviderConfig)
	if err != nil {
		logger.WithField("err", err).Error("couldn't create backend provider")
		return
	}
	logger.WithFields(logrus.Fields{
		"provider": fmt.Sprintf("%#v", provider),
	}).Debug("built")

	commandDispatcher := worker.NewCommandDispatcher(ctx, amqpConn)
	logger.WithFields(logrus.Fields{
		"command_dispatcher": fmt.Sprintf("%#v", commandDispatcher),
	}).Debug("built")

	go commandDispatcher.Run()

	pool := worker.NewProcessorPool(cfg.Hostname, ctx, cfg.HardTimeout, amqpConn,
		provider, generator, commandDispatcher)

	pool.SkipShutdownOnLogTimeout = cfg.SkipShutdownOnLogTimeout
	logger.WithFields(logrus.Fields{
		"pool": pool,
	}).Debug("built")

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1)
	go func() {
		for {
			select {
			case sig := <-signalChan:
				if sig == syscall.SIGINT {
					logger.Info("SIGINT received, starting graceful shutdown")
					pool.GracefulShutdown()
				} else if sig == syscall.SIGTERM {
					logger.Info("SIGTERM received, shutting down immediately")
					cancel()
				} else if sig == syscall.SIGUSR1 {
					logger.WithFields(logrus.Fields{
						"version":   worker.VersionString,
						"revision":  worker.RevisionString,
						"generated": worker.GeneratedString,
						"boot_time": bootTime,
						"uptime":    time.Since(bootTime),
					}).Info("SIGUSR1 received, dumping info")
					pool.Each(func(n int, proc *worker.Processor) {
						logger.WithFields(logrus.Fields{
							"n":         n,
							"id":        proc.ID,
							"job":       fmt.Sprintf("%#v", proc.CurrentJob),
							"processed": proc.ProcessedCount,
						}).Info("processor info")
					})
				} else {
					logger.WithField("signal", sig).Info("ignoring unknown signal")
				}
			default:
				time.Sleep(time.Second)
			}
		}
	}()

	logger.WithFields(logrus.Fields{
		"pool_size":  cfg.PoolSize,
		"queue_name": cfg.QueueName,
	}).Debug("running pool")

	pool.Run(cfg.PoolSize, cfg.QueueName)

	err = amqpConn.Close()
	if err != nil {
		logger.WithField("err", err).Error("couldn't close AMQP connection cleanly")
		return
	}
}
