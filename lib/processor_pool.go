package lib

import (
	"sync"

	"github.com/Sirupsen/logrus"
	"github.com/streadway/amqp"
	"github.com/travis-ci/worker/lib/backend"
	"golang.org/x/net/context"
)

// A ProcessorPool spins up multiple Processors handling build jobs from the
// same queue.
type ProcessorPool struct {
	Context   context.Context
	Conn      *amqp.Connection
	Provider  backend.Provider
	Generator BuildScriptGenerator
	Logger    *logrus.Entry

	processorsLock sync.Mutex
	processors     []*Processor
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
	for _, processor := range p.processors {
		processor.GracefulShutdown()
	}
	p.processorsLock.Unlock()
}

func (p *ProcessorPool) processor(queue *JobQueue) {
	buildJobChan, err := queue.Jobs()
	if err != nil {
		// TODO: log
		return
	}

	proc := NewProcessor(p.Context, buildJobChan, p.Provider, p.Generator, p.Logger)

	p.processorsLock.Lock()
	p.processors = append(p.processors, proc)
	p.processorsLock.Unlock()

	proc.Run()
}
