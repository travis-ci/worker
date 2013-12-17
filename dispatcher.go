package main

import (
	"encoding/json"
	"sync"
)

type Dispatcher struct {
	mb      MessageBroker
	workers map[int64]*Worker
	logger  *Logger
	rwMutex sync.RWMutex
}

type dispatchedCommand struct {
	CommandType string `json:"type"`
}

type cancelCommand struct {
	JobID  int64  `json:"job_id"`
	Source string `json:"source"`
}

func NewDispatcher(mb MessageBroker, logger *Logger) *Dispatcher {
	dispatcher := &Dispatcher{
		mb:      mb,
		workers: make(map[int64]*Worker),
		logger:  logger,
	}

	go func() {
		logger.Info("starting commands subscriber")
		mb.SubscribeFanout("worker.commands", func() MessageProcessor {
			return dispatcher
		})
	}()

	return dispatcher
}

func (d *Dispatcher) Register(worker *Worker, jobID int64) {
	d.rwMutex.Lock()
	defer d.rwMutex.Unlock()

	d.workers[jobID] = worker
}

func (d *Dispatcher) Deregister(jobID int64) {
	d.rwMutex.Lock()
	defer d.rwMutex.Unlock()

	delete(d.workers, jobID)
}

func (d *Dispatcher) Process(payload []byte) {
	var command dispatchedCommand
	err := json.Unmarshal(payload, &command)
	if err != nil {
		return
	}

	switch command.CommandType {
	case "cancel_job":
		d.handleCancel(payload)
	case "":
		d.logger.Warn("type not present")
	default:
		d.logger.Warnf("type:%s not recognized", command.CommandType)
	}

	return
}

func (d *Dispatcher) handleCancel(payload []byte) {
	var command cancelCommand
	err := json.Unmarshal(payload, &command)
	if err != nil {
		d.logger.Set("command", "cancel").Error(err.Error())
	}

	d.logger.Infof("received cancel for job id:%d, source:%s", command.JobID, command.Source)

	d.rwMutex.RLock()
	defer d.rwMutex.RUnlock()
	worker, ok := d.workers[command.JobID]
	if !ok {
		return
	}

	d.logger.Infof("worker running job id:%d found, canceling now", command.JobID)

	worker.Cancel <- true
}
