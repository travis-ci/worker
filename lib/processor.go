package lib

import (
	"time"

	"code.google.com/p/go-uuid/uuid"

	"github.com/Sirupsen/logrus"
	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/lib/backend"
	"golang.org/x/net/context"
)

// InstanceStartTimeout is the timeout for starting an instance and waiting for
// it to be available.
var InstanceStartTimeout = 300 * time.Second

// JobTimeout is the maximum time a job can take before it times out.
var JobTimeout = 50 * time.Minute

// A Processor will process build jobs on a channel, one by one, until it is
// told to shut down or the channel of build jobs closes.
type Processor struct {
	processorUUID uuid.UUID

	ctx           context.Context
	buildJobsChan <-chan Job
	provider      backend.Provider
	generator     BuildScriptGenerator
	logger        *logrus.Entry

	graceful  chan struct{}
	terminate context.CancelFunc
}

// NewProcessor creates a new processor that will run the build jobs on the
// given channel using the given provider and getting build scripts from the
// generator.
func NewProcessor(ctx context.Context, buildJobsChan <-chan Job, provider backend.Provider, generator BuildScriptGenerator, logger *logrus.Entry) *Processor {
	processorUUID := uuid.NewRandom()

	ctx, cancel := context.WithCancel(contextFromProcessor(ctx, processorUUID.String()))

	return &Processor{
		processorUUID: processorUUID,

		ctx:           contextFromProcessor(ctx, processorUUID.String()),
		buildJobsChan: buildJobsChan,
		provider:      provider,
		generator:     generator,
		logger:        logger.WithField("processor", processorUUID.String()),

		graceful:  make(chan struct{}),
		terminate: cancel,
	}
}

// Run starts the processor. This method will not return until the processor is
// terminated, either by calling the GracefulShutdown or Terminate methods, or
// if the build jobs channel is closed.
func (p *Processor) Run() {
	for {
		select {
		case <-p.graceful:
			p.logger.Info("processor is done, terminating")
			return
		case buildJob, ok := <-p.buildJobsChan:
			if !ok {
				return
			}
			p.process(p.ctx, buildJob)
		}
	}
}

// GracefulShutdown tells the processor to finish the job it is currently
// processing, but not pick up any new jobs. This method will return
// immediately, the processor is done when Run() returns.
func (p *Processor) GracefulShutdown() {
	p.logger.Info("processor initiating graceful shutdown")
	close(p.graceful)
}

// Terminate tells the processor to stop working on the current job as soon as
// possible.
func (p *Processor) Terminate() {
	p.terminate()
}

func (p *Processor) process(ctx context.Context, buildJob Job) {
	state := new(multistep.BasicStateBag)
	state.Put("buildJob", buildJob)
	state.Put("logger", p.logger)
	state.Put("ctx", ctx)

	steps := []multistep.Step{
		&stepGenerateScript{generator: p.generator},
		&stepStartInstance{provider: p.provider},
		&stepUploadScript{},
		&stepRunScript{},
	}

	runner := &multistep.BasicRunner{Steps: steps}

	runner.Run(state)
}
