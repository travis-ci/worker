package worker

import (
	"fmt"
	"time"

	gocontext "context"

	"github.com/mitchellh/multistep"
	"github.com/pborman/uuid"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
)

// A Processor gets jobs off the job queue and coordinates running it with other
// components.
type Processor struct {
	ID       uuid.UUID
	hostname string

	hardTimeout         time.Duration
	initialSleep        time.Duration
	logTimeout          time.Duration
	maxLogLength        int
	scriptUploadTimeout time.Duration
	startupTimeout      time.Duration
	payloadFilterScript string

	ctx                     gocontext.Context
	buildJobsChan           <-chan Job
	provider                backend.Provider
	generator               BuildScriptGenerator
	cancellationBroadcaster *CancellationBroadcaster

	graceful  chan struct{}
	terminate gocontext.CancelFunc

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
	HardTimeout         time.Duration
	InitialSleep        time.Duration
	LogTimeout          time.Duration
	MaxLogLength        int
	ScriptUploadTimeout time.Duration
	StartupTimeout      time.Duration
	PayloadFilterScript string
}

// NewProcessor creates a new processor that will run the build jobs on the
// given channel using the given provider and getting build scripts from the
// generator.
func NewProcessor(ctx gocontext.Context, hostname string, queue JobQueue,
	provider backend.Provider, generator BuildScriptGenerator, cancellationBroadcaster *CancellationBroadcaster,
	config ProcessorConfig) (*Processor, error) {

	uuidString, _ := context.ProcessorFromContext(ctx)
	processorUUID := uuid.Parse(uuidString)

	ctx, cancel := gocontext.WithCancel(ctx)

	buildJobsChan, err := queue.Jobs(ctx)
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't create jobs channel")
		cancel()
		return nil, err
	}

	return &Processor{
		ID:       processorUUID,
		hostname: hostname,

		initialSleep:        config.InitialSleep,
		hardTimeout:         config.HardTimeout,
		logTimeout:          config.LogTimeout,
		scriptUploadTimeout: config.ScriptUploadTimeout,
		startupTimeout:      config.StartupTimeout,
		maxLogLength:        config.MaxLogLength,
		payloadFilterScript: config.PayloadFilterScript,

		ctx:                     ctx,
		buildJobsChan:           buildJobsChan,
		provider:                provider,
		generator:               generator,
		cancellationBroadcaster: cancellationBroadcaster,

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
			logger.Info("processor is done, terminating")
			p.terminate()
			return
		default:
		}

		select {
		case <-p.ctx.Done():
			logger.Info("processor is done, terminating")
			return
		case <-p.graceful:
			logger.Info("processor is done, terminating")
			p.terminate()
			return
		case buildJob, ok := <-p.buildJobsChan:
			if !ok {
				p.terminate()
				return
			}

			hardTimeout := p.hardTimeout
			if buildJob.Payload().Timeouts.HardLimit != 0 {
				hardTimeout = time.Duration(buildJob.Payload().Timeouts.HardLimit) * time.Second
			}
			buildJob.StartAttributes().HardTimeout = hardTimeout

			ctx := context.FromJobID(context.FromRepository(p.ctx, buildJob.Payload().Repository.Slug), buildJob.Payload().Job.ID)
			if buildJob.Payload().UUID != "" {
				ctx = context.FromUUID(ctx, buildJob.Payload().UUID)
			}
			ctx, cancel := gocontext.WithTimeout(ctx, hardTimeout)
			p.LastJobID = buildJob.Payload().Job.ID
			p.CurrentStatus = "processing"
			p.process(ctx, buildJob)
			p.CurrentStatus = "waiting"
			cancel()
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
	tryClose(p.graceful)
}

// Terminate tells the processor to stop working on the current job as soon as
// possible.
func (p *Processor) Terminate() {
	p.terminate()
}

func (p *Processor) process(ctx gocontext.Context, buildJob Job) {
	state := new(multistep.BasicStateBag)
	state.Put("hostname", p.fullHostname())
	state.Put("buildJob", buildJob)
	state.Put("ctx", ctx)

	logger := context.LoggerFromContext(ctx).WithField("self", "processor")

	logTimeout := p.logTimeout
	if buildJob.Payload().Timeouts.LogSilence != 0 {
		logTimeout = time.Duration(buildJob.Payload().Timeouts.LogSilence) * time.Second
	}

	steps := []multistep.Step{
		&stepSubscribeCancellation{
			cancellationBroadcaster: p.cancellationBroadcaster,
		},
		&stepTransformBuildJSON{
			payloadFilterScript: p.payloadFilterScript,
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

func (p *Processor) fullHostname() string {
	return fmt.Sprintf("%s:%s", p.hostname, p.ID)
}
