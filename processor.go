package worker

import (
	"fmt"
	"time"

	"github.com/mitchellh/multistep"
	"github.com/pborman/uuid"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
	gocontext "golang.org/x/net/context"
)

// A Processor gets jobs off the job queue and coordinates running it with other
// components.
type Processor struct {
	ID       uuid.UUID
	hostname string

	hardTimeout         time.Duration
	logTimeout          time.Duration
	scriptUploadTimeout time.Duration
	startupTimeout      time.Duration

	ctx           gocontext.Context
	buildJobsChan <-chan Job
	provider      backend.Provider
	generator     BuildScriptGenerator
	canceller     Canceller

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

// NewProcessor creates a new processor that will run the build jobs on the
// given channel using the given provider and getting build scripts from the
// generator.
func NewProcessor(ctx gocontext.Context, hostname string, queue JobQueue,
	provider backend.Provider, generator BuildScriptGenerator, canceller Canceller,
	hardTimeout, logTimeout, scriptUploadTimeout, startupTimeout time.Duration) (*Processor, error) {

	uuidString, _ := context.ProcessorFromContext(ctx)
	processorUUID := uuid.Parse(uuidString)

	ctx, cancel := gocontext.WithCancel(ctx)

	buildJobsChan, err := queue.Jobs(ctx)
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't create jobs channel")
		return nil, err
	}

	return &Processor{
		ID:       processorUUID,
		hostname: hostname,

		hardTimeout:         hardTimeout,
		logTimeout:          logTimeout,
		scriptUploadTimeout: scriptUploadTimeout,
		startupTimeout:      startupTimeout,

		ctx:           ctx,
		buildJobsChan: buildJobsChan,
		provider:      provider,
		generator:     generator,
		canceller:     canceller,

		graceful:  make(chan struct{}),
		terminate: cancel,

		CurrentStatus: "new",
	}, nil
}

// Run starts the processor. This method will not return until the processor is
// terminated, either by calling the GracefulShutdown or Terminate methods, or
// if the build jobs channel is closed.
func (p *Processor) Run() {
	context.LoggerFromContext(p.ctx).Info("starting processor")
	defer context.LoggerFromContext(p.ctx).Info("processor done")

	for {
		select {
		case <-p.ctx.Done():
			context.LoggerFromContext(p.ctx).Info("processor is done, terminating")
			return
		case <-p.graceful:
			context.LoggerFromContext(p.ctx).Info("processor is done, terminating")
			return
		default:
		}

		select {
		case <-p.ctx.Done():
			context.LoggerFromContext(p.ctx).Info("processor is done, terminating")
			p.CurrentStatus = "done"
			return
		case <-p.graceful:
			context.LoggerFromContext(p.ctx).Info("processor is done, terminating")
			p.CurrentStatus = "done"
			return
		case buildJob, ok := <-p.buildJobsChan:
			if !ok {
				p.CurrentStatus = "done"
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
	defer func() {
		err := recover()
		if err != nil {
			context.LoggerFromContext(p.ctx).WithField("err", err).Error("recovered from panic")
		}
	}()
	context.LoggerFromContext(p.ctx).Info("processor initiating graceful shutdown")
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

	logTimeout := p.logTimeout
	if buildJob.Payload().Timeouts.LogSilence != 0 {
		logTimeout = time.Duration(buildJob.Payload().Timeouts.LogSilence) * time.Second
	}

	steps := []multistep.Step{
		&stepSubscribeCancellation{
			canceller: p.canceller,
		},
		&stepGenerateScript{
			generator: p.generator,
		},
		&stepSendReceived{},
		&stepStartInstance{
			provider:     p.provider,
			startTimeout: p.startupTimeout,
		},
		&stepUploadScript{
			uploadTimeout: p.scriptUploadTimeout,
		},
		&stepUpdateState{},
		&stepOpenLogWriter{
			logTimeout:   logTimeout,
			maxLogLength: 4500000,
		},
		&stepRunScript{
			logTimeout:               logTimeout,
			hardTimeout:              p.hardTimeout,
			skipShutdownOnLogTimeout: p.SkipShutdownOnLogTimeout,
		},
	}

	runner := &multistep.BasicRunner{Steps: steps}

	context.LoggerFromContext(ctx).Info("starting job")
	runner.Run(state)
	context.LoggerFromContext(ctx).Info("finished job")
	p.ProcessedCount++
}

func (p *Processor) fullHostname() string {
	return fmt.Sprintf("%s:%s", p.hostname, p.ID)
}
