package worker

import (
	"strings"
	"sync"
	"time"

	gocontext "context"

	"github.com/Sirupsen/logrus"
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

// Jobs returns a Job channel that selects over each source queue Job channel
func (msjq *MultiSourceJobQueue) Jobs(ctx gocontext.Context) (outChan <-chan Job, err error) {
	msjq.buildJobChansMutex.Lock()
	defer msjq.buildJobChansMutex.Unlock()
	logger := context.LoggerFromContext(ctx)

	buildJobChan := make(chan Job)
	outChan = buildJobChan

	msjq.buildJobChans = []<-chan Job{}
	for _, queue := range msjq.queues {
		jc, err := queue.Jobs(ctx)
		if err != nil {
			logger.WithFields(logrus.Fields{
				"err":  err,
				"name": queue.Name(),
			}).Error("failed to get job chan from queue")
			return nil, err
		}
		msjq.buildJobChans = append(msjq.buildJobChans, jc)
	}

	go func() {
		for {
			for _, bjc := range msjq.buildJobChans {
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

// Name builds a name from each source queue name
func (msjq *MultiSourceJobQueue) Name() string {
	s := []string{}
	for _, queue := range msjq.queues {
		s = append(s, queue.Name())
	}

	return strings.Join(s, ",")
}

// Cleanup runs cleanup for each source queue
func (msjq *MultiSourceJobQueue) Cleanup() error {
	for _, queue := range msjq.queues {
		err := queue.Cleanup()
		if err != nil {
			return err
		}
	}
	return nil
}
