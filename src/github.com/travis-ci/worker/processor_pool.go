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

	processorsLock sync.Mutex
	processors     []*Processor
}

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

// Run starts up a number of processors and connects them to the given queue.
// This method stalls until all processors have finished.
func (p *ProcessorPool) Run(poolSize int, queueName string) error {
	queue, err := NewJobQueue(p.Conn, queueName)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup

	poolErrors := []error{}

	for i := 0; i < poolSize; i++ {
		wg.Add(1)
		go func() {
			err := p.processor(queue)
			if err != nil {
				poolErrors = append(poolErrors, err)
				return
			}
			wg.Done()
		}()
	}

	if len(poolErrors) > 0 {
		context.LoggerFromContext(p.Context).WithFields(logrus.Fields{
			"pool_errors": poolErrors,
		}).Panic("failed to populate pool")
	}

	wg.Wait()

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

func (p *ProcessorPool) processor(queue *JobQueue) error {
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
