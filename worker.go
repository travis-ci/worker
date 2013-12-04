package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

type Worker struct {
	Name           string
	api            VMCloudAPI
	jobQueue       *JobQueue
	reporter       *Reporter
	currentPayload Payload
	once           sync.Once
	logger         *log.Logger
	stopChan       chan bool
	doneChan       chan bool
}

func NewWorker(name string, api VMCloudAPI, jobQueue *JobQueue, reporter *Reporter) *Worker {
	return &Worker{
		Name:     name,
		api:      api,
		jobQueue: jobQueue,
		stopChan: make(chan bool),
		doneChan: make(chan bool, 1),
		logger:   log.New(os.Stdout, fmt.Sprintf("%s: ", name), log.Ldate|log.Ltime),
		reporter: reporter,
	}
}

func (w *Worker) Start() {
	w.once.Do(func() {
		w.logger.Println("Starting worker")
		for {
			select {
			case payload, ok := <-w.jobQueue.PayloadChannel():
				if !ok {
					w.logger.Println("AMQP channel closed, stopping worker")
					w.jobQueue.Shutdown()
					w.doneChan <- true
					return
				}
				w.logger.Printf("Starting job slug:%s id:%d", payload.Repository.Slug, payload.Job.Id)
				w.currentPayload = payload
				if w.run() {
					payload.Ack()
				} else {
					payload.Nack()
				}
			case <-w.stopChan:
				w.logger.Println("Stopping worker")
				w.jobQueue.Shutdown()
				w.doneChan <- true
				return
			}
		}
	})
}

func (w *Worker) run() bool {
	w.reporter.NotifyJobStarted(w.currentPayload.Job.Id)
	server, err := w.bootServer()
	if err != nil {
		w.logger.Printf("Booting a VM failed with the following errors: %v\n", err)
		return false
	}
	defer server.Destroy()

	w.logger.Println("Opening SSH connection")
	ssh, err := NewSSHConnection(server)
	if err != nil {
		w.logger.Printf("Couldn't connect to SSH: %v\n", err)
		return false
	}
	defer ssh.Close()

	w.logger.Println("Uploading build script")
	err = w.uploadScript(ssh)
	if err != nil {
		w.logger.Printf("Couldn't upload script to SSH: %v\n", err)
		return false
	}

	w.logger.Println("Running the build")
	outputChan, err := w.runScript(ssh)
	if err != nil {
		w.logger.Printf("Failed to run build script: %v\n", err)
		return false
	}

	for {
		select {
		case bytes, ok := <-outputChan:
			if !ok {
				w.logger.Println("Build finished.")
				w.reporter.SendFinal(w.currentPayload.Job.Id)
				w.reporter.NotifyJobFinished(w.currentPayload.Job.Id, "passed")
				return true
			}
			if bytes != nil {
				w.reporter.SendLog(w.currentPayload.Job.Id, string(bytes))
			}
		case <-time.After(10 * time.Second):
			w.logger.Println("No log output after 10 seconds, stopping build")
			w.reporter.SendLog(w.currentPayload.Job.Id, "\n\nYour build didn't produce any output in the last 10 seconds, stopping the job.\n")
			w.reporter.SendFinal(w.currentPayload.Job.Id)
			w.reporter.NotifyJobFinished(w.currentPayload.Job.Id, "errored")
			return false
		}
	}
}

func (w *Worker) bootServer() (VMCloudServer, error) {
	startTime := time.Now()
	hostname := fmt.Sprintf("testing-worker-go-%d-%s-%d", os.Getpid(), w.Name, w.currentPayload.Job.Id)
	w.logger.Printf("Booting %s\n", hostname)
	server, err := w.api.Start(hostname)
	if err != nil {
		return nil, err
	}

	doneChan, cancelChan := waitFor(func() bool {
		server.Refresh()
		return server.Ready()
	}, 3*time.Second)

	select {
	case <-doneChan:
		w.logger.Printf("VM provisioned in %.2f seconds\n", time.Now().Sub(startTime).Seconds())
	case <-time.After(4 * time.Minute):
		cancelChan <- true
		return nil, errors.New("VM could not boot within 4 minutes")
	}

	return server, nil
}

func (w *Worker) uploadScript(ssh *SSHConnection) error {
	err := ssh.UploadFile("~/build.sh", []byte(fmt.Sprintf("echo This is build id %d", w.currentPayload.Job.Id)))
	if err != nil {
		return err
	}

	err = ssh.Run("chmod +x ~/build.sh")
	if err != nil {
		return err
	}

	return nil
}

func (w *Worker) runScript(ssh *SSHConnection) (chan []byte, error) {
	return ssh.Start("~/build.sh")
}
