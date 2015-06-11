package worker

import (
	"fmt"
	"time"

	"code.google.com/p/go-uuid/uuid"

	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
	gocontext "golang.org/x/net/context"
)

// A Processor will process build jobs on a channel, one by one, until it is
// told to shut down or the channel of build jobs closes.
type Processor struct {
	processorUUID uuid.UUID
	hostname      string
	hardTimeout   time.Duration

	ctx           gocontext.Context
	buildJobsChan <-chan Job
	provider      backend.Provider
	generator     BuildScriptGenerator
	canceller     Canceller

	graceful  chan struct{}
	terminate gocontext.CancelFunc

	SkipShutdownOnLogTimeout bool
}

// NewProcessor creates a new processor that will run the build jobs on the
// given channel using the given provider and getting build scripts from the
// generator.
func NewProcessor(ctx gocontext.Context, hostname string, buildJobsQueue *JobQueue,
	provider backend.Provider, generator BuildScriptGenerator, canceller Canceller,
	hardTimeout time.Duration) (*Processor, error) {

	processorUUID := uuid.NewRandom()

	ctx, cancel := gocontext.WithCancel(context.FromProcessor(ctx, processorUUID.String()))

	buildJobsChan, err := buildJobsQueue.Jobs(ctx)
	if err != nil {
		return nil, err
	}

	return &Processor{
		processorUUID: processorUUID,
		hostname:      hostname,
		hardTimeout:   hardTimeout,

		ctx:           context.FromProcessor(ctx, processorUUID.String()),
		buildJobsChan: buildJobsChan,
		provider:      provider,
		generator:     generator,
		canceller:     canceller,

		graceful:  make(chan struct{}),
		terminate: cancel,
	}, nil
}

// Run starts the processor. This method will not return until the processor is
// terminated, either by calling the GracefulShutdown or Terminate methods, or
// if the build jobs channel is closed.
func (p *Processor) Run() {
	context.LoggerFromContext(p.ctx).Info("starting processor")

	for {
		select {
		case <-p.ctx.Done():
			context.LoggerFromContext(p.ctx).Info("processor is done, terminating")
			return
		case <-p.graceful:
			context.LoggerFromContext(p.ctx).Info("processor is done, terminating")
			return
		case buildJob, ok := <-p.buildJobsChan:
			if !ok {
				return
			}
			ctx, cancel := gocontext.WithTimeout(context.FromUUID(context.FromJobID(context.FromRepository(p.ctx, buildJob.Payload().Repository.Slug), buildJob.Payload().Job.ID), buildJob.Payload().UUID), p.hardTimeout)
			p.process(ctx, buildJob)
			cancel()
		}
	}

	context.LoggerFromContext(p.ctx).Info("processor done")
}

// GracefulShutdown tells the processor to finish the job it is currently
// processing, but not pick up any new jobs. This method will return
// immediately, the processor is done when Run() returns.
func (p *Processor) GracefulShutdown() {
	context.LoggerFromContext(p.ctx).Info("processor initiating graceful shutdown")
	close(p.graceful)
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
			startTimeout: 4 * time.Minute,
		},
		&stepUploadScript{},
		&stepUpdateState{},
		&stepRunScript{
			logTimeout:               10 * time.Minute,
			maxLogLength:             4500000,
			hardTimeout:              p.hardTimeout,
			skipShutdownOnLogTimeout: p.SkipShutdownOnLogTimeout,
		},
	}

	runner := &multistep.BasicRunner{Steps: steps}

	context.LoggerFromContext(ctx).Info("starting job")
	runner.Run(state)
	context.LoggerFromContext(ctx).Info("finished job")
}

func (p *Processor) fullHostname() string {
	return fmt.Sprintf("%s:%s", p.hostname, p.processorUUID)
}
