package main

import (
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/exec"
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
	travismetrics "github.com/travis-ci/worker/metrics"
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
	app.Copyright = worker.CopyrightString

	app.Flags = config.Flags
	app.Action = runWorker

	app.Run(os.Args)
}

func runWorker(c *cli.Context) {
	if os.Getenv("START_HOOK") != "" {
		_ = exec.Command("/bin/sh", "-c", os.Getenv("START_HOOK")).Run()
	}

	if os.Getenv("STOP_HOOK") != "" {
		defer exec.Command("/bin/sh", "-c", os.Getenv("STOP_HOOK")).Run()
	}

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

	cfg := config.FromCLIContext(c)

	if c.Bool("echo-config") {
		config.WriteEnvConfig(cfg, os.Stdout)
		return
	}

	if c.Bool("list-providers") {
		backend.EachBackend(func(b *backend.Backend) {
			fmt.Println(b.Alias)
		})
		return
	}

	logger.WithFields(logrus.Fields{
		"cfg": fmt.Sprintf("%#v", cfg),
	}).Debug("read config")

	logger.Info("worker started")
	defer logger.Info("worker finished")

	setupSentry(logger, cfg)
	setupMetrics(logger, cfg, c)

	jobQueue, canceller, err := setupJobQueueAndCanceller(logger, cfg, ctx, cancel)
	if err != nil {
		logger.WithField("err", err).Error("couldn't create job queue and canceller")
		return
	}

	generator := worker.NewBuildScriptGenerator(cfg)
	logger.WithFields(logrus.Fields{
		"build_script_generator": fmt.Sprintf("%#v", generator),
	}).Debug("built")

	provider, err := backend.NewBackendProvider(cfg.ProviderName, cfg.ProviderConfig)
	if err != nil {
		logger.WithField("err", err).Error("couldn't create backend provider")
		return
	}
	logger.WithFields(logrus.Fields{
		"provider": fmt.Sprintf("%#v", provider),
	}).Debug("built")

	pool := worker.NewProcessorPool(cfg.Hostname, ctx, cfg.HardTimeout, cfg.LogTimeout,
		provider, generator, canceller)

	pool.SkipShutdownOnLogTimeout = cfg.SkipShutdownOnLogTimeout
	logger.WithFields(logrus.Fields{
		"pool": pool,
	}).Debug("built")

	go signalHandler(logger, pool, cancel)

	logger.WithFields(logrus.Fields{
		"pool_size": cfg.PoolSize,
		"queue":     jobQueue,
	}).Debug("running pool")

	pool.Run(cfg.PoolSize, jobQueue)

	err = jobQueue.Cleanup()
	if err != nil {
		logger.WithField("err", err).Error("couldn't clean up job queue")
		return
	}
}

func setupSentry(logger *logrus.Entry, cfg *config.Config) {
	if cfg.SentryDSN != "" {
		sentryHook, err := logrus_sentry.NewSentryHook(cfg.SentryDSN, []logrus.Level{logrus.PanicLevel, logrus.FatalLevel, logrus.ErrorLevel})
		if err != nil {
			logger.WithField("err", err).Error("couldn't create sentry hook")
		}

		logrus.AddHook(sentryHook)
	}
}

func setupMetrics(logger *logrus.Entry, cfg *config.Config, c *cli.Context) {
	go travismetrics.ReportMemstatsMetrics()
	if cfg.LibratoEmail != "" && cfg.LibratoToken != "" && cfg.LibratoSource != "" {
		logger.Info("starting librato metrics reporter")
		go librato.Librato(metrics.DefaultRegistry, time.Minute, cfg.LibratoEmail, cfg.LibratoToken, cfg.LibratoSource, []float64{0.95}, time.Millisecond)
	} else if !c.Bool("silence-metrics") {
		logger.Info("starting logger metrics reporter")
		go metrics.Log(metrics.DefaultRegistry, time.Minute, log.New(os.Stderr, "metrics: ", log.Lmicroseconds))
	}
}

func signalHandler(logger *logrus.Entry, pool *worker.ProcessorPool, cancel gocontext.CancelFunc) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM, syscall.SIGINT,
		syscall.SIGUSR1, syscall.SIGTTIN, syscall.SIGTTOU)
	for {
		select {
		case sig := <-signalChan:
			switch sig {
			case syscall.SIGINT:
				logger.Info("SIGINT received, starting graceful shutdown")
				pool.GracefulShutdown()
			case syscall.SIGTERM:
				logger.Info("SIGTERM received, shutting down immediately")
				cancel()
			case syscall.SIGTTIN:
				logger.Info("SIGTTIN received, adding processor to pool")
				pool.Incr()
			case syscall.SIGTTOU:
				logger.Info("SIGTTOU received, removing processor from pool")
				pool.Decr()
			case syscall.SIGUSR1:
				logger.WithFields(logrus.Fields{
					"version":   worker.VersionString,
					"revision":  worker.RevisionString,
					"generated": worker.GeneratedString,
					"boot_time": bootTime.String(),
					"uptime":    time.Since(bootTime),
					"pool_size": pool.Size(),
				}).Info("SIGUSR1 received, dumping info")
				pool.Each(func(n int, proc *worker.Processor) {
					logger.WithFields(logrus.Fields{
						"n":         n,
						"id":        proc.ID,
						"processed": proc.ProcessedCount,
					}).Info("processor info")
				})
			default:
				logger.WithField("signal", sig).Info("ignoring unknown signal")
			}
		default:
			time.Sleep(time.Second)
		}
	}
}

func setupJobQueueAndCanceller(logger *logrus.Entry, cfg *config.Config, ctx gocontext.Context, cancel gocontext.CancelFunc) (worker.JobQueue, worker.Canceller, error) {
	switch cfg.QueueType {
	case "amqp":
		amqpConn, err := amqp.Dial(cfg.AmqpURI)
		if err != nil {
			logger.WithField("err", err).Error("couldn't connect to AMQP")
			return nil, nil, err
		}

		go amqpErrorWatcher(amqpConn, logger, cancel)

		logger.Debug("connected to AMQP")

		canceller := worker.NewAMQPCanceller(ctx, amqpConn)
		logger.WithFields(logrus.Fields{
			"canceller": fmt.Sprintf("%#v", canceller),
		}).Debug("built")

		go canceller.Run()

		jobQueue, err := worker.NewAMQPJobQueue(amqpConn, cfg.QueueName)
		if err != nil {
			return nil, nil, err
		}

		return jobQueue, canceller, nil
	case "file":
		canceller := worker.NewFileCanceller(ctx, cfg.BaseDir)
		go canceller.Run()

		jobQueue, err := worker.NewFileJobQueue(cfg.BaseDir, cfg.QueueName)
		if err != nil {
			return nil, nil, err
		}

		return jobQueue, canceller, nil
	}

	return nil, nil, fmt.Errorf("unknown queue type %q", cfg.QueueType)
}

func amqpErrorWatcher(amqpConn *amqp.Connection, logger *logrus.Entry, cancel gocontext.CancelFunc) {
	errChan := make(chan *amqp.Error)
	errChan = amqpConn.NotifyClose(errChan)

	err, ok := <-errChan
	if ok {
		logger.WithField("err", err).Error("amqp connection errored, terminating")
		cancel()
	}
}
