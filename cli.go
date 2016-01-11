package worker

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	// include for conditional pprof HTTP server
	_ "net/http/pprof"

	"github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	"github.com/mihasya/go-metrics-librato"
	"github.com/rcrowley/go-metrics"
	"github.com/streadway/amqp"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
	travismetrics "github.com/travis-ci/worker/metrics"
	gocontext "golang.org/x/net/context"
)

// CLI is the top level of execution for the whole shebang
type CLI struct {
	c        *cli.Context
	bootTime time.Time

	ctx    gocontext.Context
	cancel gocontext.CancelFunc
	logger *logrus.Entry

	Config               *config.Config
	BuildScriptGenerator BuildScriptGenerator
	BackendProvider      backend.Provider
	ProcessorPool        *ProcessorPool
	Canceller            Canceller
	JobQueue             JobQueue
}

// NewCLI creates a new *CLI from a *cli.Context
func NewCLI(c *cli.Context) *CLI {
	return &CLI{
		c:        c,
		bootTime: time.Now().UTC(),
	}
}

// Configure parses and sets configuration from the CLI context
func (i *CLI) Configure() {
	i.Config = config.FromCLIContext(i.c)
}

// Setup runs one-time preparatory actions and returns a boolean success value
// that is used to determine if it is safe to invoke the Run func
func (i *CLI) Setup() (bool, error) {
	if i.c.String("pprof-port") != "" {
		// Start net/http/pprof server
		go func() {
			http.ListenAndServe(fmt.Sprintf("localhost:%s", i.c.String("pprof-port")), nil)
		}()
	}

	if i.c.Bool("debug") {
		logrus.SetLevel(logrus.DebugLevel)
	}

	ctx, cancel := gocontext.WithCancel(gocontext.Background())
	logger := context.LoggerFromContext(ctx)

	i.ctx = ctx
	i.cancel = cancel
	i.logger = logger

	logrus.SetFormatter(&logrus.TextFormatter{DisableColors: true})

	i.Configure()
	cfg := i.Config

	logger.WithFields(logrus.Fields{
		"cfg": fmt.Sprintf("%#v", cfg),
	}).Debug("read config")

	i.setupSentry()
	i.setupMetrics()

	err := i.setupJobQueueAndCanceller()
	if err != nil {
		logger.WithField("err", err).Error("couldn't create job queue and canceller")
		return false, err
	}

	generator := NewBuildScriptGenerator(cfg)
	logger.WithFields(logrus.Fields{
		"build_script_generator": fmt.Sprintf("%#v", generator),
	}).Debug("built")

	i.BuildScriptGenerator = generator

	provider, err := backend.NewBackendProvider(cfg.ProviderName, i.c)
	if err != nil {
		logger.WithField("err", err).Error("couldn't create backend provider")
		return false, err
	}

	err = provider.Setup()
	if err != nil {
		logger.WithField("err", err).Error("couldn't setup backend provider")
		return false, err
	}

	logger.WithFields(logrus.Fields{
		"provider": fmt.Sprintf("%#v", provider),
	}).Debug("built")

	i.BackendProvider = provider

	ppc := &ProcessorPoolConfig{
		Hostname:            cfg.Hostname,
		Context:             ctx,
		HardTimeout:         cfg.HardTimeout,
		LogTimeout:          cfg.LogTimeout,
		ScriptUploadTimeout: cfg.ScriptUploadTimeout,
		StartupTimeout:      cfg.StartupTimeout,
	}

	pool := NewProcessorPool(ppc, i.BackendProvider, i.BuildScriptGenerator, i.Canceller)

	pool.SkipShutdownOnLogTimeout = cfg.SkipShutdownOnLogTimeout
	logger.WithFields(logrus.Fields{
		"pool": pool,
	}).Debug("built")

	i.ProcessorPool = pool

	return true, nil
}

// Run starts all long-running processes and blocks until the processor pool
// returns from its Run func
func (i *CLI) Run() {
	if os.Getenv("START_HOOK") != "" {
		_ = exec.Command("/bin/sh", "-c", os.Getenv("START_HOOK")).Run()
	}

	if os.Getenv("STOP_HOOK") != "" {
		defer exec.Command("/bin/sh", "-c", os.Getenv("STOP_HOOK")).Run()
	}

	i.logger.Info("worker started")
	defer i.logger.Info("worker finished")

	go i.signalHandler()

	i.logger.WithFields(logrus.Fields{
		"pool_size": i.Config.PoolSize,
		"queue":     i.JobQueue,
	}).Debug("running pool")

	i.ProcessorPool.Run(i.Config.PoolSize, i.JobQueue)

	err := i.JobQueue.Cleanup()
	if err != nil {
		i.logger.WithField("err", err).Error("couldn't clean up job queue")
	}
}

func (i *CLI) setupSentry() {
	if i.Config.SentryDSN == "" {
		return
	}

	levels := []logrus.Level{
		logrus.PanicLevel,
		logrus.FatalLevel,
	}

	if i.Config.SentryHookErrors {
		levels = append(levels, logrus.ErrorLevel)
	}

	sentryHook, err := NewSentryHook(i.Config.SentryDSN, levels)

	if err != nil {
		i.logger.WithField("err", err).Error("couldn't create sentry hook")
	}

	logrus.AddHook(sentryHook)
}

func (i *CLI) setupMetrics() {
	go travismetrics.ReportMemstatsMetrics()

	if i.Config.LibratoEmail != "" && i.Config.LibratoToken != "" && i.Config.LibratoSource != "" {
		i.logger.Info("starting librato metrics reporter")

		go librato.Librato(metrics.DefaultRegistry, time.Minute,
			i.Config.LibratoEmail, i.Config.LibratoToken, i.Config.LibratoSource,
			[]float64{0.50, 0.75, 0.90, 0.95, 0.99, 0.999, 1.0}, time.Millisecond)
	} else if !i.c.Bool("silence-metrics") {
		i.logger.Info("starting logger metrics reporter")

		go metrics.Log(metrics.DefaultRegistry, time.Minute,
			log.New(os.Stderr, "metrics: ", log.Lmicroseconds))
	}
}

func (i *CLI) signalHandler() {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGTERM, syscall.SIGINT,
		syscall.SIGUSR1, syscall.SIGTTIN, syscall.SIGTTOU)
	for {
		select {
		case sig := <-signalChan:
			switch sig {
			case syscall.SIGINT:
				i.logger.Info("SIGINT received, starting graceful shutdown")
				i.ProcessorPool.GracefulShutdown()
			case syscall.SIGTERM:
				i.logger.Info("SIGTERM received, shutting down immediately")
				i.cancel()
			case syscall.SIGTTIN:
				i.logger.Info("SIGTTIN received, adding processor to pool")
				i.ProcessorPool.Incr()
			case syscall.SIGTTOU:
				i.logger.Info("SIGTTOU received, removing processor from pool")
				i.ProcessorPool.Decr()
			case syscall.SIGUSR1:
				i.logger.WithFields(logrus.Fields{
					"version":   VersionString,
					"revision":  RevisionString,
					"generated": GeneratedString,
					"boot_time": i.bootTime.String(),
					"uptime":    time.Since(i.bootTime),
					"pool_size": i.ProcessorPool.Size(),
				}).Info("SIGUSR1 received, dumping info")
				i.ProcessorPool.Each(func(n int, proc *Processor) {
					i.logger.WithFields(logrus.Fields{
						"n":           n,
						"id":          proc.ID,
						"processed":   proc.ProcessedCount,
						"status":      proc.CurrentStatus,
						"last_job_id": proc.LastJobID,
					}).Info("processor info")
				})
			default:
				i.logger.WithField("signal", sig).Info("ignoring unknown signal")
			}
		default:
			time.Sleep(time.Second)
		}
	}
}

func (i *CLI) setupJobQueueAndCanceller() error {
	switch i.Config.QueueType {
	case "amqp":
		amqpConn, err := amqp.Dial(i.Config.AmqpURI)
		if err != nil {
			i.logger.WithField("err", err).Error("couldn't connect to AMQP")
			return err
		}

		go i.amqpErrorWatcher(amqpConn)

		i.logger.Debug("connected to AMQP")

		canceller := NewAMQPCanceller(i.ctx, amqpConn)
		i.logger.WithFields(logrus.Fields{
			"canceller": fmt.Sprintf("%#v", canceller),
		}).Debug("built")

		i.Canceller = canceller

		go canceller.Run()

		jobQueue, err := NewAMQPJobQueue(amqpConn, i.Config.QueueName)
		if err != nil {
			return err
		}

		jobQueue.DefaultLanguage = i.Config.DefaultLanguage
		jobQueue.DefaultDist = i.Config.DefaultDist
		jobQueue.DefaultGroup = i.Config.DefaultGroup
		jobQueue.DefaultOS = i.Config.DefaultOS

		i.JobQueue = jobQueue
		return nil
	case "file":
		canceller := NewFileCanceller(i.ctx, i.Config.BaseDir)
		go canceller.Run()

		i.Canceller = canceller

		jobQueue, err := NewFileJobQueue(i.Config.BaseDir, i.Config.QueueName, i.Config.FilePollingInterval)
		if err != nil {
			return err
		}

		jobQueue.DefaultLanguage = i.Config.DefaultLanguage
		jobQueue.DefaultDist = i.Config.DefaultDist
		jobQueue.DefaultGroup = i.Config.DefaultGroup
		jobQueue.DefaultOS = i.Config.DefaultOS

		i.JobQueue = jobQueue
		return nil
	}

	return fmt.Errorf("unknown queue type %q", i.Config.QueueType)
}

func (i *CLI) amqpErrorWatcher(amqpConn *amqp.Connection) {
	errChan := make(chan *amqp.Error)
	errChan = amqpConn.NotifyClose(errChan)

	err, ok := <-errChan
	if ok {
		i.logger.WithField("err", err).Error("amqp connection errored, terminating")
		i.cancel()
	}
}
