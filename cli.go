package worker

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
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

	// include for conditional pprof HTTP server
	_ "net/http/pprof"

	gocontext "context"

	"github.com/cenk/backoff"
	"github.com/getsentry/raven-go"
	"github.com/mihasya/go-metrics-librato"
	"github.com/pkg/errors"
	"github.com/rcrowley/go-metrics"
	"github.com/sirupsen/logrus"
	"github.com/streadway/amqp"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/config"
	"github.com/travis-ci/worker/context"
	travismetrics "github.com/travis-ci/worker/metrics"
	cli "gopkg.in/urfave/cli.v1"
)

var (
	rootContext = gocontext.TODO()
)

// CLI is the top level of execution for the whole shebang
type CLI struct {
	c        *cli.Context
	id       string
	bootTime time.Time

	ctx    gocontext.Context
	cancel gocontext.CancelFunc
	logger *logrus.Entry

	Config                  *config.Config
	BuildScriptGenerator    BuildScriptGenerator
	BackendProvider         backend.Provider
	ProcessorPool           *ProcessorPool
	CancellationBroadcaster *CancellationBroadcaster
	JobQueue                JobQueue

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

		CancellationBroadcaster: NewCancellationBroadcaster(),
	}
}

// Setup runs one-time preparatory actions and returns a boolean success value
// that is used to determine if it is safe to invoke the Run func
func (i *CLI) Setup() (bool, error) {
	if i.c.Bool("debug") {
		logrus.SetLevel(logrus.DebugLevel)
	}

	ctx, cancel := gocontext.WithCancel(gocontext.Background())
	logger := context.LoggerFromContext(ctx).WithField("self", "cli")

	i.ctx = ctx
	rootContext = ctx
	i.cancel = cancel
	i.logger = logger

	logrus.SetFormatter(&logrus.TextFormatter{DisableColors: true})

	i.Config = config.FromCLIContext(i.c)

	if i.c.String("pprof-port") != "" && i.c.String("http-api-port") != "" {
		return false, fmt.Errorf("only one http port is allowed. "+
			"pprof-port=%v http-api-port=%v",
			i.c.String("pprof-port"), i.c.String("http-api-port"))
	}
	if i.c.String("pprof-port") != "" || i.c.String("http-api-port") != "" {
		if i.c.String("http-api-auth") != "" {
			i.setupHTTPAPI()
		} else {
			i.logger.Info("skipping HTTP API setup without http-api-auth set")
		}
		go func() {
			httpPort := i.c.String("http-api-port")
			if httpPort == "" {
				httpPort = i.c.String("pprof-port")
			}
			httpAddr := fmt.Sprintf("localhost:%s", httpPort)
			i.logger.Info("listening at ", httpAddr)
			http.ListenAndServe(httpAddr, nil)
		}()
	}

	err := i.setupWorkerID()
	if err != nil {
		return false, err
	}

	if i.c.Bool("echo-config") {
		config.WriteEnvConfig(i.Config, os.Stdout)
		return false, nil
	}

	if i.c.Bool("list-backend-providers") {
		backend.EachBackend(func(b *backend.Backend) {
			fmt.Println(b.Alias)
		})
		return false, nil
	}

	logger.WithField("cfg", fmt.Sprintf("%#v", i.Config)).Debug("read config")

	i.setupSentry()
	i.setupMetrics()

	generator := NewBuildScriptGenerator(i.Config)
	logger.WithField("build_script_generator", fmt.Sprintf("%#v", generator)).Debug("built")

	i.BuildScriptGenerator = generator

	provider, err := backend.NewBackendProvider(i.Config.ProviderName, i.Config.ProviderConfig)
	if err != nil {
		logger.WithField("err", err).Error("couldn't create backend provider")
		return false, err
	}

	err = provider.Setup(ctx)
	if err != nil {
		logger.WithField("err", err).Error("couldn't setup backend provider")
		return false, err
	}

	logger.WithField("provider", fmt.Sprintf("%#v", provider)).Debug("built")

	i.BackendProvider = provider

	ppc := &ProcessorPoolConfig{
		Hostname: i.Config.Hostname,
		Context:  ctx,

		HardTimeout:         i.Config.HardTimeout,
		InitialSleep:        i.Config.InitialSleep,
		LogTimeout:          i.Config.LogTimeout,
		MaxLogLength:        i.Config.MaxLogLength,
		ScriptUploadTimeout: i.Config.ScriptUploadTimeout,
		StartupTimeout:      i.Config.StartupTimeout,
		PayloadFilterScript: i.Config.FilterJobJsonScript,
	}

	pool := NewProcessorPool(ppc, i.BackendProvider, i.BuildScriptGenerator, i.CancellationBroadcaster)

	pool.SkipShutdownOnLogTimeout = i.Config.SkipShutdownOnLogTimeout
	logger.WithField("pool", pool).Debug("built")

	i.ProcessorPool = pool

	err = i.setupJobQueueAndCanceller(pool)
	if err != nil {
		logger.WithField("err", err).Error("couldn't create job queue and canceller")
		return false, err
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
	defer i.logProcessorInfo("worker finished")

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

func (i *CLI) setupWorkerID() error {
	randomBytes := make([]byte, 32)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return errors.Wrap(err, "error reading random bytes")
	}

	hash := sha1.Sum(randomBytes)
	i.id = fmt.Sprintf("worker+%s@%d.%s",
		hex.EncodeToString(hash[:]), os.Getpid(), i.Config.Hostname)
	return nil
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
	}).Error("stop hook failed")
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

	raven.SetRelease(VersionString)
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

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode > 299 {
		return fmt.Errorf("unhappy status code %d", resp.StatusCode)
	}

	body := map[string]string{}
	err = json.NewDecoder(resp.Body).Decode(&body)
	if err != nil {
		return err
	}

	if state, ok := body["state"]; ok && state == "down" {
		i.logger.WithField("heartbeat_state", state).Info("starting graceful shutdown")
		i.ProcessorPool.GracefulShutdown(false)
	}
	return nil
}

func (i *CLI) setupHTTPAPI() {
	i.logger.Info("setting up HTTP API")
	http.HandleFunc("/worker", i.httpAPI)
	http.HandleFunc("/worker/", i.httpAPI)
}

func (i *CLI) httpAPI(w http.ResponseWriter, req *http.Request) {
	if req.Method == "GET" && req.URL.Path == "/worker" {
		w.Header().Set("Content-Type", "text/plain;charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, strings.TrimSpace(`
Available methods:

- POST /worker/graceful-shutdown
- POST /worker/graceful-shutdown-pause
- POST /worker/info
- POST /worker/pool-decr
- POST /worker/pool-incr
- POST /worker/shutdown
		`)+"\n")
		return
	}

	if req.Method != "POST" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	username, password, ok := req.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", "Basic realm=\"travis-ci/worker\"")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	authBytes := []byte(fmt.Sprintf("%s:%s", username, password))
	if subtle.ConstantTimeCompare(authBytes, []byte(i.c.String("http-api-auth"))) != 1 {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "text/plain;charset=utf-8")

	action := strings.ToLower(strings.Replace(req.URL.Path, "/worker/", "", 1))
	i.logger.Info(fmt.Sprintf("web %s received", action))
	switch action {
	case "graceful-shutdown":
		i.ProcessorPool.GracefulShutdown(false)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "starting graceful shutdown\n")
	case "shutdown":
		i.cancel()
		fmt.Fprintf(w, "shutting down immediately\n")
	case "pool-incr":
		prev := i.ProcessorPool.Size()
		i.ProcessorPool.Incr()
		fmt.Fprintf(w, "adding processor to pool (%v + 1)\n", prev)
	case "pool-decr":
		prev := i.ProcessorPool.Size()
		i.ProcessorPool.Decr()
		fmt.Fprintf(w, "removing processor from pool (%v - 1)\n", prev)
	case "graceful-shutdown-pause":
		i.ProcessorPool.GracefulShutdown(true)
		fmt.Fprintf(w, "toggling graceful shutdown and pause\n")
	case "info":
		fmt.Fprintf(w, "version: %s\n"+
			"revision: %s\n"+
			"generated: %s\n"+
			"boot_time: %s\n"+
			"uptime: %v\n"+
			"pool_size: %v\n"+
			"total_processed: %v\n"+
			"processors:\n",
			VersionString,
			RevisionString,
			GeneratedString,
			i.bootTime.String(),
			time.Since(i.bootTime),
			i.ProcessorPool.Size(),
			i.ProcessorPool.TotalProcessed())
		i.ProcessorPool.Each(func(n int, proc *Processor) {
			fmt.Fprintf(w, "- n: %v\n"+
				"  id: %v\n"+
				"  processed: %v\n"+
				"  status: %v\n"+
				"  last_job_id: %v\n",
				n,
				proc.ID,
				proc.ProcessedCount,
				proc.CurrentStatus,
				proc.LastJobID)
		})
	default:
		w.Header().Set("Travis-Worker-Unknown-Action", action)
		w.WriteHeader(http.StatusNotFound)
	}
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
				i.logProcessorInfo("received SIGUSR1")
			default:
				i.logger.WithField("signal", sig).Info("ignoring unknown signal")
			}
		default:
			time.Sleep(time.Second)
		}
	}
}

func (i *CLI) logProcessorInfo(msg string) {
	if msg == "" {
		msg = "processor pool info"
	}
	i.logger.WithFields(logrus.Fields{
		"version":         VersionString,
		"revision":        RevisionString,
		"generated":       GeneratedString,
		"boot_time":       i.bootTime.String(),
		"uptime":          time.Since(i.bootTime),
		"pool_size":       i.ProcessorPool.Size(),
		"total_processed": i.ProcessorPool.TotalProcessed(),
	}).Info(msg)
	i.ProcessorPool.Each(func(n int, proc *Processor) {
		i.logger.WithFields(logrus.Fields{
			"n":           n,
			"id":          proc.ID,
			"processed":   proc.ProcessedCount,
			"status":      proc.CurrentStatus,
			"last_job_id": proc.LastJobID,
		}).Info("processor info")
	})
}

func (i *CLI) setupJobQueueAndCanceller(pool *ProcessorPool) error {
	subQueues := []JobQueue{}
	for _, queueType := range strings.Split(i.Config.QueueType, ",") {
		queueType = strings.TrimSpace(queueType)

		switch queueType {
		case "amqp":
			jobQueue, canceller, err := i.buildAMQPJobQueueAndCanceller()
			if err != nil {
				return err
			}
			go canceller.Run()
			subQueues = append(subQueues, jobQueue)
		case "file":
			jobQueue, err := i.buildFileJobQueue()
			if err != nil {
				return err
			}
			subQueues = append(subQueues, jobQueue)
		case "http":
			jobQueue, err := i.buildHTTPJobQueue(pool)
			if err != nil {
				return err
			}
			subQueues = append(subQueues, jobQueue)
		default:
			return fmt.Errorf("unknown queue type %q", queueType)
		}
	}

	if len(subQueues) == 0 {
		return fmt.Errorf("no queues built")
	}

	if len(subQueues) == 1 {
		i.JobQueue = subQueues[0]
	} else {
		i.JobQueue = NewMultiSourceJobQueue(subQueues...)
	}
	return nil
}

func (i *CLI) buildAMQPJobQueueAndCanceller() (*AMQPJobQueue, *AMQPCanceller, error) {
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
				return nil, nil, err
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
		return nil, nil, err
	}

	go i.amqpErrorWatcher(amqpConn)

	i.logger.Debug("connected to AMQP")

	canceller := NewAMQPCanceller(i.ctx, amqpConn, i.CancellationBroadcaster)
	i.logger.WithField("canceller", fmt.Sprintf("%#v", canceller)).Debug("built")

	jobQueue, err := NewAMQPJobQueue(amqpConn, i.Config.QueueName)
	if err != nil {
		return nil, nil, err
	}

	jobQueue.DefaultLanguage = i.Config.DefaultLanguage
	jobQueue.DefaultDist = i.Config.DefaultDist
	jobQueue.DefaultGroup = i.Config.DefaultGroup
	jobQueue.DefaultOS = i.Config.DefaultOS

	return jobQueue, canceller, nil
}

func (i *CLI) buildHTTPJobQueue(pool *ProcessorPool) (*HTTPJobQueue, error) {
	jobBoardURL, err := url.Parse(i.Config.JobBoardURL)
	if err != nil {
		return nil, errors.Wrap(err, "error parsing job board URL")
	}

	jobQueue, err := NewHTTPJobQueue(
		pool, jobBoardURL, i.Config.TravisSite,
		i.Config.ProviderName, i.Config.QueueName, i.id)
	if err != nil {
		return nil, errors.Wrap(err, "error creating HTTP job queue")
	}

	jobQueue.DefaultLanguage = i.Config.DefaultLanguage
	jobQueue.DefaultDist = i.Config.DefaultDist
	jobQueue.DefaultGroup = i.Config.DefaultGroup
	jobQueue.DefaultOS = i.Config.DefaultOS

	return jobQueue, nil
}

func (i *CLI) buildFileJobQueue() (*FileJobQueue, error) {
	jobQueue, err := NewFileJobQueue(i.Config.BaseDir, i.Config.QueueName, i.Config.FilePollingInterval)
	if err != nil {
		return nil, err
	}

	jobQueue.DefaultLanguage = i.Config.DefaultLanguage
	jobQueue.DefaultDist = i.Config.DefaultDist
	jobQueue.DefaultGroup = i.Config.DefaultGroup
	jobQueue.DefaultOS = i.Config.DefaultOS

	return jobQueue, nil
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
