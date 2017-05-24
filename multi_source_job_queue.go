package worker

import (
	"sync"
	"time"

	gocontext "context"

	"github.com/travis-ci/worker/context"
)

type MultiSourceJobQueue struct {
	queues             []JobQueue
	buildJobChans      []<-chan Job
	buildJobChansMutex *sync.Mutex
}

func NewMultiSourceJobQueue(queues ...JobQueue) *MultiSourceJobQueue {
	return &MultiSourceJobQueue{
		queues:             queues,
		buildJobChansMutex: &sync.Mutex{},
	}
}

func (tjq *MultiSourceJobQueue) Jobs(ctx gocontext.Context) (outChan <-chan Job, err error) {
	tjq.buildJobChansMutex.Lock()
	defer tjq.buildJobChansMutex.Unlock()
	logger := context.LoggerFromContext(ctx)

	buildJobChan := make(chan Job)
	outChan = buildJobChan

	go func() {
		tjq.buildJobChans = []<-chan Job{}
		for _, queue := range tjq.queues {
			jc, err := queue.Jobs(ctx)
			if err != nil {
				logger.WithField("err", err).Error("failed to get job chan from queue")
				return
			}
			tjq.buildJobChans = append(tjq.buildJobChans, jc)
		}

		for {
			for _, bjc := range tjq.buildJobChans {
				select {
				case job := <-bjc:
					buildJobChan <- job
				default:
					time.Sleep(time.Millisecond)
				}
			}
		}
	}()

	return outChan, nil
}

func (tjq *MultiSourceJobQueue) Cleanup() error {
	for _, queue := range tjq.queues {
		err := queue.Cleanup()
		if err != nil {
			return err
		}
	}
	return nil
}
