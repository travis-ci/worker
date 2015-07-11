package worker

import (
	"sort"
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/streadway/amqp"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
	gocontext "golang.org/x/net/context"
)

// A ProcessorPool spins up multiple Processors handling build jobs from the
// same queue.
type ProcessorPool struct {
	Context     gocontext.Context
	Conn        *amqp.Connection
	Provider    backend.Provider
	Generator   BuildScriptGenerator
	Canceller   Canceller
	Hostname    string
	HardTimeout time.Duration
	LogTimeout  time.Duration

	SkipShutdownOnLogTimeout bool

	queue          *JobQueue
	poolErrors     []error
	processorsLock sync.Mutex
	processors     []*Processor
	processorsWG   sync.WaitGroup
}

// NewProcessorPool creates a new processor pool using the given arguments.
func NewProcessorPool(hostname string, ctx gocontext.Context, hardTimeout time.Duration,
	logTimeout time.Duration, amqpConn *amqp.Connection, provider backend.Provider,
	generator BuildScriptGenerator, canceller Canceller) *ProcessorPool {

	return &ProcessorPool{
		Hostname:    hostname,
		Context:     ctx,
		HardTimeout: hardTimeout,
		LogTimeout:  logTimeout,
		Conn:        amqpConn,
		Provider:    provider,
		Generator:   generator,
		Canceller:   canceller,
	}
}

// Each loops through all the processors in the pool and calls the given
// function for each of them, passing in the index and the processor. The order
// of the processors is the same for the same set of processors.
func (p *ProcessorPool) Each(f func(int, *Processor)) {
	procIDs := []string{}
	procsByID := map[string]*Processor{}

	for _, proc := range p.processors {
		id := proc.ID.String()
		procIDs = append(procIDs, id)
		procsByID[id] = proc
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

// Run starts up a number of processors and connects them to the given queue.
// This method stalls until all processors have finished.
func (p *ProcessorPool) Run(poolSize int, queueName string) error {
	queue, err := NewJobQueue(p.Conn, queueName)
	if err != nil {
		return err
	}

	p.queue = queue
	p.poolErrors = []error{}

	for i := 0; i < poolSize; i++ {
		p.Incr()
	}

	if len(p.poolErrors) > 0 {
		context.LoggerFromContext(p.Context).WithFields(logrus.Fields{
			"pool_errors": p.poolErrors,
		}).Panic("failed to populate pool")
	}

	p.processorsWG.Wait()

	return nil
}

// GracefulShutdown causes each processor in the pool to start its graceful
// shutdown.
func (p *ProcessorPool) GracefulShutdown() {
	p.processorsLock.Lock()
	defer p.processorsLock.Unlock()

	for _, processor := range p.processors {
		processor.GracefulShutdown()
	}
}

// Incr adds a single running processor to the pool
func (p *ProcessorPool) Incr() {
	p.processorsWG.Add(1)
	go func() {
		err := p.runProcessor(p.queue)
		if err != nil {
			p.poolErrors = append(p.poolErrors, err)
			return
		}
		p.processorsWG.Done()
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

func (p *ProcessorPool) runProcessor(queue *JobQueue) error {
	proc, err := NewProcessor(p.Context, p.Hostname, queue, p.Provider, p.Generator, p.Canceller, p.HardTimeout, p.LogTimeout)
	if err != nil {
		context.LoggerFromContext(p.Context).WithField("err", err).Error("couldn't create processor")
		return err
	}

	proc.SkipShutdownOnLogTimeout = p.SkipShutdownOnLogTimeout

	p.processorsLock.Lock()
	p.processors = append(p.processors, proc)
	p.processorsLock.Unlock()

	proc.Run()
	return nil
}
