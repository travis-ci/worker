package lib

import (
	"sync"
	"time"

	"github.com/streadway/amqp"
	"github.com/travis-ci/worker/lib/backend"
	"github.com/travis-ci/worker/lib/context"
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

	SkipShutdownOnLogTimeout bool

	processorsLock sync.Mutex
	processors     []*Processor
}

func NewProcessorPool(hostname string, ctx gocontext.Context, hardTimeout time.Duration,
	amqpConn *amqp.Connection, provider backend.Provider, generator BuildScriptGenerator,
	canceller Canceller) *ProcessorPool {

	return &ProcessorPool{
		Hostname:    hostname,
		Context:     ctx,
		HardTimeout: hardTimeout,
		Conn:        amqpConn,
		Provider:    provider,
		Generator:   generator,
		Canceller:   canceller,
	}
}

// Run starts up a number of processors and connects them to the given queue.
// This method stalls until all processors have finished.
func (p *ProcessorPool) Run(poolSize uint16, queueName string) error {
	queue, err := NewJobQueue(p.Conn, queueName)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup

	var i uint16
	for i = 0; i < poolSize; i++ {
		wg.Add(1)
		go func() {
			p.processor(queue)
			wg.Done()
		}()
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

func (p *ProcessorPool) processor(queue *JobQueue) {
	proc, err := NewProcessor(p.Context, p.Hostname, queue, p.Provider, p.Generator, p.Canceller, p.HardTimeout)
	if err != nil {
		context.LoggerFromContext(p.Context).WithField("err", err).Error("couldn't create processor")
	}

	proc.SkipShutdownOnLogTimeout = p.SkipShutdownOnLogTimeout

	p.processorsLock.Lock()
	p.processors = append(p.processors, proc)
	p.processorsLock.Unlock()

	proc.Run()
}
