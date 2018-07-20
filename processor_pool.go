package worker

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	gocontext "context"

	"github.com/pborman/uuid"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
)

// A ProcessorPool spins up multiple Processors handling build jobs from the
// same queue.
type ProcessorPool struct {
	Context                 gocontext.Context
	Provider                backend.Provider
	Generator               BuildScriptGenerator
	CancellationBroadcaster *CancellationBroadcaster
	Hostname                string

	HardTimeout, InitialSleep, LogTimeout, ScriptUploadTimeout, StartupTimeout time.Duration
	MaxLogLength                                                               int

	PayloadFilterExecutable string

	SkipShutdownOnLogTimeout bool

	queue            JobQueue
	logWriterFactory LogWriterFactory
	poolErrors       []error
	processorsLock   sync.Mutex
	processors       []*Processor
	processorsWG     sync.WaitGroup
	pauseCount       int
}

type ProcessorPoolConfig struct {
	Hostname string
	Context  gocontext.Context

	HardTimeout, InitialSleep, LogTimeout, ScriptUploadTimeout, StartupTimeout time.Duration
	MaxLogLength                                                               int

	PayloadFilterExecutable string
}

// NewProcessorPool creates a new processor pool using the given arguments.
func NewProcessorPool(ppc *ProcessorPoolConfig,
	provider backend.Provider, generator BuildScriptGenerator,
	cancellationBroadcaster *CancellationBroadcaster) *ProcessorPool {

	return &ProcessorPool{
		Hostname: ppc.Hostname,
		Context:  ppc.Context,

		HardTimeout:         ppc.HardTimeout,
		InitialSleep:        ppc.InitialSleep,
		LogTimeout:          ppc.LogTimeout,
		ScriptUploadTimeout: ppc.ScriptUploadTimeout,
		StartupTimeout:      ppc.StartupTimeout,
		MaxLogLength:        ppc.MaxLogLength,

		Provider:                provider,
		Generator:               generator,
		CancellationBroadcaster: cancellationBroadcaster,
		PayloadFilterExecutable: ppc.PayloadFilterExecutable,
	}
}

// Each loops through all the processors in the pool and calls the given
// function for each of them, passing in the index and the processor. The order
// of the processors is the same for the same set of processors.
func (p *ProcessorPool) Each(f func(int, *Processor)) {
	procIDs := []string{}
	procsByID := map[string]*Processor{}

	for _, proc := range p.processors {
		procIDs = append(procIDs, proc.ID)
		procsByID[proc.ID] = proc
	}

	sort.Strings(procIDs)

	for i, procID := range procIDs {
		f(i, procsByID[procID])
	}
}

// Size returns the number of processors in the pool
func (p *ProcessorPool) Size() int {
	return len(p.processors)
}

// TotalProcessed returns the sum of all processor ProcessedCount values.
func (p *ProcessorPool) TotalProcessed() int {
	total := 0
	p.Each(func(_ int, pr *Processor) {
		total += pr.ProcessedCount
	})
	return total
}

// Run starts up a number of processors and connects them to the given queue.
// This method stalls until all processors have finished.
func (p *ProcessorPool) Run(poolSize int, queue JobQueue, logWriterFactory LogWriterFactory) error {
	p.queue = queue
	p.logWriterFactory = logWriterFactory
	p.poolErrors = []error{}

	for i := 0; i < poolSize; i++ {
		p.Incr()
	}

	if len(p.poolErrors) > 0 {
		context.LoggerFromContext(p.Context).WithFields(logrus.Fields{
			"self":        "processor_pool",
			"pool_errors": p.poolErrors,
		}).Panic("failed to populate pool")
	}

	p.processorsWG.Wait()

	return nil
}

// GracefulShutdown causes each processor in the pool to start its graceful
// shutdown.
func (p *ProcessorPool) GracefulShutdown(togglePause bool) {
	p.processorsLock.Lock()
	defer p.processorsLock.Unlock()

	logger := context.LoggerFromContext(p.Context).WithField("self", "processor_pool")

	if togglePause {
		p.pauseCount++

		if p.pauseCount == 1 {
			logger.Info("incrementing wait group for pause")
			p.processorsWG.Add(1)
		} else if p.pauseCount == 2 {
			logger.Info("finishing wait group to unpause")
			p.processorsWG.Done()
		} else if p.pauseCount > 2 {
			return
		}
	}

	for _, processor := range p.processors {
		processor.GracefulShutdown()
	}
}

// Incr adds a single running processor to the pool
func (p *ProcessorPool) Incr() {
	p.processorsWG.Add(1)
	go func() {
		defer p.processorsWG.Done()
		err := p.runProcessor(p.queue, p.logWriterFactory)
		if err != nil {
			p.poolErrors = append(p.poolErrors, err)
			return
		}
	}()
}

// Decr pops a processor out of the pool and issues a graceful shutdown
func (p *ProcessorPool) Decr() {
	if len(p.processors) == 0 {
		return
	}

	var proc *Processor
	proc, p.processors = p.processors[len(p.processors)-1], p.processors[:len(p.processors)-1]
	proc.GracefulShutdown()
}

func (p *ProcessorPool) runProcessor(queue JobQueue, logWriterFactory LogWriterFactory) error {
	processorUUID := uuid.NewRandom()
	processorID := fmt.Sprintf("%s@%d.%s", processorUUID.String(), os.Getpid(), p.Hostname)
	ctx := context.FromProcessor(p.Context, processorID)

	proc, err := NewProcessor(ctx, p.Hostname,
		queue, logWriterFactory, p.Provider, p.Generator, p.CancellationBroadcaster,
		ProcessorConfig{
			HardTimeout:             p.HardTimeout,
			InitialSleep:            p.InitialSleep,
			LogTimeout:              p.LogTimeout,
			MaxLogLength:            p.MaxLogLength,
			ScriptUploadTimeout:     p.ScriptUploadTimeout,
			StartupTimeout:          p.StartupTimeout,
			PayloadFilterExecutable: p.PayloadFilterExecutable,
		})

	if err != nil {
		context.LoggerFromContext(p.Context).WithFields(logrus.Fields{
			"err":  err,
			"self": "processor_pool",
		}).Error("couldn't create processor")
		return err
	}

	proc.SkipShutdownOnLogTimeout = p.SkipShutdownOnLogTimeout

	p.processorsLock.Lock()
	p.processors = append(p.processors, proc)
	p.processorsLock.Unlock()

	proc.Run()
	return nil
}
