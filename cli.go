package worker

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gopkg.in/urfave/cli.v1"

	// include for conditional pprof HTTP server
	_ "net/http/pprof"

	"github.com/Sirupsen/logrus"
	"github.com/cenk/backoff"
	"github.com/getsentry/raven-go"
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

	heartbeatErrSleep time.Duration
	heartbeatSleep    time.Duration
}

// NewCLI creates a new *CLI from a *cli.Context
func NewCLI(c *cli.Context) *CLI {
	return &CLI{
		c:        c,
		bootTime: time.Now().UTC(),

		heartbeatSleep:    5 * time.Minute,
		heartbeatErrSleep: 30 * time.Second,
	}
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

	cfg := config.FromCLIContext(i.c)
	i.Config = cfg

	if i.c.Bool("echo-config") {
		config.WriteEnvConfig(cfg, os.Stdout)
		return false, nil
	}

	if i.c.Bool("list-backend-providers") {
		backend.EachBackend(func(b *backend.Backend) {
			fmt.Println(b.Alias)
		})
		return false, nil
	}

	logger.WithFields(logrus.Fields{
		"cfg": fmt.Sprintf("%#v", cfg),
	}).Debug("read config")

	i.setupSentry()
	i.setupMetrics()

	if i.Config.QueueType != "http" {
		err := i.setupJobQueueAndCanceller()
		if err != nil {
			logger.WithField("err", err).Error("couldn't create job queue and canceller")
			return false, err
		}
	}

	generator := NewBuildScriptGenerator(cfg)
	logger.WithFields(logrus.Fields{
		"build_script_generator": fmt.Sprintf("%#v", generator),
	}).Debug("built")

	i.BuildScriptGenerator = generator

	provider, err := backend.NewBackendProvider(cfg.ProviderName, cfg.ProviderConfig)
	if err != nil {
		logger.WithField("err", err).Error("couldn't create backend provider")
		return false, err
	}

	err = provider.Setup(ctx)
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
		MaxLogLength:        cfg.MaxLogLength,
	}

	pool := NewProcessorPool(ppc, i.BackendProvider, i.BuildScriptGenerator, i.Canceller)

	pool.SkipShutdownOnLogTimeout = cfg.SkipShutdownOnLogTimeout
	logger.WithFields(logrus.Fields{
		"pool": pool,
	}).Debug("built")

	i.ProcessorPool = pool

	if i.Config.QueueType == "http" {
		err := i.setupJobQueueForHTTP(pool)
		if err != nil {
			logger.WithField("err", err).Error("couldn't setup job queue for http")
			return false, err
		}
	}

	return true, nil
}

// Run starts all long-running processes and blocks until the processor pool
// returns from its Run func
func (i *CLI) Run() {
	i.logger.Info("starting")

	i.handleStartHook()
	defer i.handleStopHook()

	i.logger.Info("worker started")
	defer i.logger.Info("worker finished")

	i.logger.Info("setting up heartbeat")
	i.setupHeartbeat()

	i.logger.Info("starting signal handler loop")
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

func (i *CLI) setupHeartbeat() {
	hbURL := i.c.String("heartbeat-url")
	if hbURL == "" {
		return
	}

	hbTok := i.c.String("heartbeat-url-auth-token")
	if strings.HasPrefix(hbTok, "file://") {
		hbTokBytes, err := ioutil.ReadFile(strings.Split(hbTok, "://")[1])
		if err != nil {
			i.logger.WithField("err", err).Error("failed to read auth token from file")
		} else {
			hbTok = string(hbTokBytes)
		}
	}

	i.logger.WithField("heartbeat_url", hbURL).Info("starting heartbeat loop")
	go i.heartbeatHandler(hbURL, strings.TrimSpace(hbTok))
}

func (i *CLI) handleStartHook() {
	hookValue := i.c.String("start-hook")
	if hookValue == "" {
		return
	}

	i.logger.WithField("start_hook", hookValue).Info("running start hook")

	parts := stringSplitSpace(hookValue)
	outErr, err := exec.Command(parts[0], parts[1:]...).CombinedOutput()
	if err == nil {
		return
	}

	i.logger.WithFields(logrus.Fields{
		"err":        err,
		"output":     string(outErr),
		"start_hook": hookValue,
	}).Error("start hook failed")
}

func (i *CLI) handleStopHook() {
	hookValue := i.c.String("stop-hook")
	if hookValue == "" {
		return
	}

	i.logger.WithField("stop_hook", hookValue).Info("running stop hook")

	parts := stringSplitSpace(hookValue)
	outErr, err := exec.Command(parts[0], parts[1:]...).CombinedOutput()
	if err == nil {
		return
	}

	i.logger.WithFields(logrus.Fields{
		"err":       err,
		"output":    string(outErr),
		"stop_hook": hookValue,
	}).Error("start hook failed")
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

	err = raven.SetDSN(i.Config.SentryDSN)
	if err != nil {
		i.logger.WithField("err", err).Error("couldn't set DSN in raven")
	}
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

func (i *CLI) heartbeatHandler(heartbeatURL, heartbeatAuthToken string) {
	b := backoff.NewExponentialBackOff()
	b.MaxInterval = 10 * time.Second
	b.MaxElapsedTime = time.Minute

	for {
		err := backoff.Retry(func() error {
			return i.heartbeatCheck(heartbeatURL, heartbeatAuthToken)
		}, b)

		if err != nil {
			i.logger.WithFields(logrus.Fields{
				"heartbeat_url": heartbeatURL,
				"err":           err,
			}).Warn("failed to get heartbeat")
			time.Sleep(i.heartbeatErrSleep)
			continue
		}

		select {
		case <-i.ctx.Done():
			return
		default:
			time.Sleep(i.heartbeatSleep)
		}
	}
}

func (i *CLI) heartbeatCheck(heartbeatURL, heartbeatAuthToken string) error {
	req, err := http.NewRequest("GET", heartbeatURL, nil)
	if err != nil {
		return err
	}

	if heartbeatAuthToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("token %s", heartbeatAuthToken))
	}

	res, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}

	if res.StatusCode > 299 {
		return fmt.Errorf("unhappy status code %d", res.StatusCode)
	}

	body := map[string]string{}
	err = json.NewDecoder(res.Body).Decode(&body)
	if err != nil {
		return err
	}

	if state, ok := body["state"]; ok && state == "down" {
		i.logger.WithField("heartbeat_state", state).Info("starting graceful shutdown")
		i.ProcessorPool.GracefulShutdown(false)
	}
	return nil
}

func (i *CLI) signalHandler() {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan,
		syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1,
		syscall.SIGTTIN, syscall.SIGTTOU,
		syscall.SIGWINCH)

	for {
		select {
		case sig := <-signalChan:
			switch sig {
			case syscall.SIGINT:
				i.logger.Info("SIGINT received, starting graceful shutdown")
				i.ProcessorPool.GracefulShutdown(false)
			case syscall.SIGTERM:
				i.logger.Info("SIGTERM received, shutting down immediately")
				i.cancel()
			case syscall.SIGTTIN:
				i.logger.Info("SIGTTIN received, adding processor to pool")
				i.ProcessorPool.Incr()
			case syscall.SIGTTOU:
				i.logger.Info("SIGTTOU received, removing processor from pool")
				i.ProcessorPool.Decr()
			case syscall.SIGWINCH:
				i.logger.Info("SIGWINCH received, toggling graceful shutdown and pause")
				i.ProcessorPool.GracefulShutdown(true)
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
		var amqpConn *amqp.Connection
		var err error

		if i.Config.AmqpTlsCert != "" || i.Config.AmqpTlsCertPath != "" {
			cfg := new(tls.Config)
			cfg.RootCAs = x509.NewCertPool()
			if i.Config.AmqpTlsCert != "" {
				cfg.RootCAs.AppendCertsFromPEM([]byte(i.Config.AmqpTlsCert))
			}
			if i.Config.AmqpTlsCertPath != "" {
				cert, err := ioutil.ReadFile(i.Config.AmqpTlsCertPath)
				if err != nil {
					return err
				}
				cfg.RootCAs.AppendCertsFromPEM(cert)
			}
			amqpConn, err = amqp.DialTLS(i.Config.AmqpURI, cfg)
		} else if i.Config.AmqpInsecure {
			amqpConn, err = amqp.DialTLS(
				i.Config.AmqpURI,
				&tls.Config{InsecureSkipVerify: true},
			)
		} else {
			amqpConn, err = amqp.Dial(i.Config.AmqpURI)
		}
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

func (i *CLI) setupJobQueueForHTTP(pool *ProcessorPool) error {
	jobBoardURL, err := url.Parse(i.Config.JobBoardURL)
	if err != nil {
		return err
	}

	jobQueue, err := NewHTTPJobQueue(pool, jobBoardURL, i.Config.QueueName)
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

func (i *CLI) amqpErrorWatcher(amqpConn *amqp.Connection) {
	errChan := make(chan *amqp.Error)
	errChan = amqpConn.NotifyClose(errChan)

	err, ok := <-errChan
	if ok {
		i.logger.WithField("err", err).Error("amqp connection errored, terminating")
		i.cancel()
		time.Sleep(time.Minute)
		i.logger.Panic("timed out waiting for shutdown after amqp connection error")
	}
}
