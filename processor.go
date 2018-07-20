package worker

import (
	"time"

	gocontext "context"

	"github.com/mitchellh/multistep"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
)

// A Processor gets jobs off the job queue and coordinates running it with other
// components.
type Processor struct {
	ID       string
	hostname string

	hardTimeout             time.Duration
	initialSleep            time.Duration
	logTimeout              time.Duration
	maxLogLength            int
	scriptUploadTimeout     time.Duration
	startupTimeout          time.Duration
	payloadFilterExecutable string

	ctx                     gocontext.Context
	buildJobsChan           <-chan Job
	provider                backend.Provider
	generator               BuildScriptGenerator
	LogsQueue               LogsQueue
	cancellationBroadcaster *CancellationBroadcaster

	graceful   chan struct{}
	terminate  gocontext.CancelFunc
	shutdownAt time.Time

	// ProcessedCount contains the number of jobs that has been processed
	// by this Processor. This value should not be modified outside of the
	// Processor.
	ProcessedCount int

	// CurrentStatus contains the current status of the processor, and can
	// be one of "new", "waiting", "processing" or "done".
	CurrentStatus string

	// LastJobID contains the ID of the last job the processor processed.
	LastJobID uint64

	SkipShutdownOnLogTimeout bool
}

type ProcessorConfig struct {
	HardTimeout             time.Duration
	InitialSleep            time.Duration
	LogTimeout              time.Duration
	MaxLogLength            int
	ScriptUploadTimeout     time.Duration
	StartupTimeout          time.Duration
	PayloadFilterExecutable string
}

// NewProcessor creates a new processor that will run the build jobs on the
// given channel using the given provider and getting build scripts from the
// generator.
func NewProcessor(ctx gocontext.Context, hostname string, queue JobQueue,
	logsQueue LogsQueue, provider backend.Provider, generator BuildScriptGenerator, cancellationBroadcaster *CancellationBroadcaster,
	config ProcessorConfig) (*Processor, error) {

	processorID, _ := context.ProcessorFromContext(ctx)

	ctx, cancel := gocontext.WithCancel(ctx)

	buildJobsChan, err := queue.Jobs(ctx)
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't create jobs channel")
		cancel()
		return nil, err
	}

	return &Processor{
		ID:       processorID,
		hostname: hostname,

		initialSleep:            config.InitialSleep,
		hardTimeout:             config.HardTimeout,
		logTimeout:              config.LogTimeout,
		scriptUploadTimeout:     config.ScriptUploadTimeout,
		startupTimeout:          config.StartupTimeout,
		maxLogLength:            config.MaxLogLength,
		payloadFilterExecutable: config.PayloadFilterExecutable,

		ctx:                     ctx,
		buildJobsChan:           buildJobsChan,
		provider:                provider,
		generator:               generator,
		cancellationBroadcaster: cancellationBroadcaster,
		logsQueue:               logsQueue,

		graceful:  make(chan struct{}),
		terminate: cancel,

		CurrentStatus: "new",
	}, nil
}

// Run starts the processor. This method will not return until the processor is
// terminated, either by calling the GracefulShutdown or Terminate methods, or
// if the build jobs channel is closed.
func (p *Processor) Run() {
	logger := context.LoggerFromContext(p.ctx).WithField("self", "processor")
	logger.Info("starting processor")
	defer logger.Info("processor done")
	defer func() { p.CurrentStatus = "done" }()

	for {
		select {
		case <-p.ctx.Done():
			logger.Info("processor is done, terminating")
			return
		case <-p.graceful:
			logger.WithField("shutdown_duration_s", time.Since(p.shutdownAt).Seconds()).Info("processor is done, terminating")
			p.terminate()
			return
		default:
		}

		select {
		case <-p.ctx.Done():
			logger.Info("processor is done, terminating")
			return
		case <-p.graceful:
			logger.WithField("shutdown_duration_s", time.Since(p.shutdownAt).Seconds()).Info("processor is done, terminating")
			p.terminate()
			return
		case buildJob, ok := <-p.buildJobsChan:
			if !ok {
				p.terminate()
				return
			}

			jobID := buildJob.Payload().Job.ID

			hardTimeout := p.hardTimeout
			if buildJob.Payload().Timeouts.HardLimit != 0 {
				hardTimeout = time.Duration(buildJob.Payload().Timeouts.HardLimit) * time.Second
			}
			logger.WithFields(logrus.Fields{
				"hard_timeout": hardTimeout,
				"job_id":       jobID,
			}).Debug("setting hard timeout")
			buildJob.StartAttributes().HardTimeout = hardTimeout

			ctx := context.FromJobID(context.FromRepository(p.ctx, buildJob.Payload().Repository.Slug), buildJob.Payload().Job.ID)
			if buildJob.Payload().UUID != "" {
				ctx = context.FromUUID(ctx, buildJob.Payload().UUID)
			}

			logger.WithFields(logrus.Fields{
				"hard_timeout": hardTimeout,
				"job_id":       jobID,
			}).Debug("getting wrapped context with timeout")
			ctx, cancel := gocontext.WithTimeout(ctx, hardTimeout)

			logger.WithFields(logrus.Fields{
				"job_id": jobID,
				"status": "processing",
			}).Debug("updating processor status and last id")
			p.LastJobID = jobID
			p.CurrentStatus = "processing"

			p.process(ctx, buildJob)

			logger.WithFields(logrus.Fields{
				"job_id": jobID,
				"status": "waiting",
			}).Debug("updating processor status")
			p.CurrentStatus = "waiting"
			cancel()
		case <-time.After(10 * time.Second):
			logger.Debug("timeout waiting for job, shutdown, or context done")
		}
	}
}

// GracefulShutdown tells the processor to finish the job it is currently
// processing, but not pick up any new jobs. This method will return
// immediately, the processor is done when Run() returns.
func (p *Processor) GracefulShutdown() {
	logger := context.LoggerFromContext(p.ctx).WithField("self", "processor")
	defer func() {
		err := recover()
		if err != nil {
			logger.WithField("err", err).Error("recovered from panic")
		}
	}()
	logger.Info("processor initiating graceful shutdown")
	p.shutdownAt = time.Now()
	tryClose(p.graceful)
}

// Terminate tells the processor to stop working on the current job as soon as
// possible.
func (p *Processor) Terminate() {
	p.terminate()
}

func (p *Processor) process(ctx gocontext.Context, buildJob Job) {
	state := new(multistep.BasicStateBag)
	state.Put("hostname", p.ID)
	state.Put("buildJob", buildJob)
	state.Put("logsQueue", p.logsQueue)
	state.Put("procCtx", buildJob.SetupContext(p.ctx))
	state.Put("ctx", buildJob.SetupContext(ctx))
	state.Put("processedAt", time.Now().UTC())

	logger := context.LoggerFromContext(ctx).WithFields(logrus.Fields{
		"job_id": buildJob.Payload().Job.ID,
		"self":   "processor",
	})

	logTimeout := p.logTimeout
	if buildJob.Payload().Timeouts.LogSilence != 0 {
		logTimeout = time.Duration(buildJob.Payload().Timeouts.LogSilence) * time.Second
	}

	steps := []multistep.Step{
		&stepSubscribeCancellation{
			cancellationBroadcaster: p.cancellationBroadcaster,
		},
		&stepTransformBuildJSON{
			payloadFilterExecutable: p.payloadFilterExecutable,
		},
		&stepGenerateScript{
			generator: p.generator,
		},
		&stepSendReceived{},
		&stepSleep{duration: p.initialSleep},
		&stepCheckCancellation{},
		&stepOpenLogWriter{
			maxLogLength:      p.maxLogLength,
			defaultLogTimeout: p.logTimeout,
		},
		&stepCheckCancellation{},
		&stepStartInstance{
			provider:     p.provider,
			startTimeout: p.startupTimeout,
		},
		&stepCheckCancellation{},
		&stepUploadScript{
			uploadTimeout: p.scriptUploadTimeout,
		},
		&stepCheckCancellation{},
		&stepUpdateState{},
		&stepWriteWorkerInfo{},
		&stepCheckCancellation{},
		&stepRunScript{
			logTimeout:               logTimeout,
			hardTimeout:              p.hardTimeout,
			skipShutdownOnLogTimeout: p.SkipShutdownOnLogTimeout,
		},
	}

	runner := &multistep.BasicRunner{Steps: steps}

	logger.Info("starting job")
	runner.Run(state)
	logger.Info("finished job")
	p.ProcessedCount++
}
