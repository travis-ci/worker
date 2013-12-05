package main

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

const (
	logOutputTimeout = 10 * time.Minute
	vmBootTimeout    = 4 * time.Minute
)

// A Worker runs a job.
type Worker struct {
	Name       string
	vmProvider VMProvider
	reporter   *Reporter
	payload    Payload
	once       sync.Once
	logger     *log.Logger
}

// NewWorker creates a new worker with the given parameters. The worker assumes
// ownership of the given API, payload and reporter and they should not be
// reused for other workers.
func NewWorker(name string, VMProvider VMProvider, payload Payload, reporter *Reporter) *Worker {
	return &Worker{
		Name:       name,
		vmProvider: VMProvider,
		payload:    payload,
		logger:     log.New(os.Stdout, fmt.Sprintf("%s: ", name), log.Ldate|log.Ltime),
		reporter:   reporter,
	}
}

// Run actually runs the job. It returns whether the job should be requeued or
// not. The worker should be discarded after this method has finished.
func (w *Worker) Run() bool {
	w.logger.Printf("Starting job slug:%s id:%d\n", w.payload.Repository.Slug, w.payload.Job.ID)
	defer w.reporter.Close()

	server, err := w.bootServer()
	if err != nil {
		w.logger.Printf("Booting a VM failed with the following errors: %v\n", err)
		return false
	}
	defer server.Destroy()

	fmt.Fprintf(w.reporter.Log, "Using worker: %s\n\n", w.Name)
	defer w.reporter.Log.Close()

	w.logger.Println("Opening SSH connection")
	ssh, err := NewSSHConnection(server)
	if err != nil {
		w.logger.Printf("Couldn't connect to SSH: %v\n", err)
		fmt.Fprintf(w.reporter.Log, "We're sorry, but there was an error with the connection to the VM.\n\nYour job will be requeued shortly.")
		return false
	}
	defer ssh.Close()

	w.logger.Println("Uploading build script")
	err = w.uploadScript(ssh)
	if err != nil {
		w.logger.Printf("Couldn't upload script to SSH: %v\n", err)
		fmt.Fprintf(w.reporter.Log, "We're sorry, but there was an error with the connection to the VM.\n\nYour job will be requeued shortly.")
		return false
	}

	err = w.reporter.NotifyJobStarted()
	if err != nil {
		w.logger.Printf("Couldn't notify about job start: %v\n", err)
		return false
	}

	w.logger.Println("Running the build")
	exitCodeChan, err := w.runScript(ssh)
	if err != nil {
		w.logger.Printf("Failed to run build script: %v\n", err)
		fmt.Fprintf(w.reporter.Log, "We're sorry, but there was an error with the connection to the VM.\n\nYour job will be requeued shortly.")
		return false
	}

	select {
	case exitCode := <-exitCodeChan:
		w.logger.Println("Build finished.")
		switch exitCode {
		case 0:
			w.reporter.NotifyJobFinished("passed")
		case 1:
			w.reporter.NotifyJobFinished("failed")
		default:
			w.reporter.NotifyJobFinished("errored")
		case -1:
			fmt.Fprintf(w.reporter.Log, "We're sorry, but there was an error with the connection to the VM.\n\nYour job will be requeued shortly.")
			return false
		}
		return true
	case <-time.After(logOutputTimeout):
		fmt.Fprintf(w.reporter.Log, "\n\nNo output has been received in the last %.0f minutes, this potentially indicates a stalled build or something wrong with the build itself.\n\nThe build has been terminated\n\n", logOutputTimeout.Minutes())
		w.reporter.NotifyJobFinished("errored")
		return true
	}
}

func (w *Worker) jobID() int64 {
	return w.payload.Job.ID
}

func (w *Worker) bootServer() (VM, error) {
	startTime := time.Now()
	hostname := fmt.Sprintf("testing-worker-go-%d-%s", os.Getpid(), w.Name)
	w.logger.Printf("Booting %s\n", hostname)
	server, err := w.vmProvider.Start(hostname, vmBootTimeout)
	if err != nil {
		return nil, err
	}

	w.logger.Printf("VM provisioned in %.2f seconds\n", time.Now().Sub(startTime).Seconds())

	return server, nil
}

func (w *Worker) uploadScript(ssh *SSHConnection) error {
	err := ssh.UploadFile("~/build.sh", []byte(fmt.Sprintf("#!/bin/bash --login\n\necho This is build id %d\necho Now sleeping to test timeout\nsleep 15", w.jobID())))
	if err != nil {
		return err
	}

	return ssh.Run("chmod +x ~/build.sh")
}

func (w *Worker) runScript(ssh *SSHConnection) (<-chan int, error) {
	return ssh.Start("~/build.sh", w.reporter.Log)
}
