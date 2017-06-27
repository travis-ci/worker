package worker

import (
	"fmt"
	"strings"
	"time"

	gocontext "context"

	"github.com/Sirupsen/logrus"
	"github.com/travis-ci/worker/context"
)

type MultiSourceJobQueue struct {
	queues []JobQueue
}

func NewMultiSourceJobQueue(queues ...JobQueue) *MultiSourceJobQueue {
	return &MultiSourceJobQueue{queues: queues}
}

// Jobs returns a Job channel that selects over each source queue Job channel
func (msjq *MultiSourceJobQueue) Jobs(ctx gocontext.Context) (outChan <-chan Job, err error) {
	logger := context.LoggerFromContext(ctx).WithField("self", "multi_source_job_queue")

	buildJobChan := make(chan Job)
	outChan = buildJobChan

	buildJobChans := map[string]<-chan Job{}
	for i, queue := range msjq.queues {
		jc, err := queue.Jobs(ctx)
		if err != nil {
			logger.WithFields(logrus.Fields{
				"err":  err,
				"name": queue.Name(),
			}).Error("failed to get job chan from queue")
			return nil, err
		}
		buildJobChans[fmt.Sprintf("%s.%d", queue.Name(), i)] = jc
	}

	go func() {
		for {
			for queueName, bjc := range buildJobChans {
				select {
				case <-ctx.Done():
					return
				case job := <-bjc:
					logger.WithField("source", queueName).Info("sending job to multi source output")
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
