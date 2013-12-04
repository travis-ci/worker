package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// A Worker runs a job.
type Worker struct {
	Name     string
	api      VMCloudAPI
	reporter *Reporter
	payload  Payload
	once     sync.Once
	logger   *log.Logger
}

// NewWorker creates a new worker with the given parameters. The worker assumes
// ownership of the given API, payload and reporter and they should not be
// reused for other workers.
func NewWorker(name string, api VMCloudAPI, payload Payload, reporter *Reporter) *Worker {
	return &Worker{
		Name:     name,
		api:      api,
		payload:  payload,
		logger:   log.New(os.Stdout, fmt.Sprintf("%s: ", name), log.Ldate|log.Ltime),
		reporter: reporter,
	}
}

// Run actually runs the job. It returns whether the job should be requeued or
// not. The worker should be discarded after this method has finished.
func (w *Worker) Run() bool {
	defer w.reporter.Close()

	server, err := w.bootServer()
	if err != nil {
		w.logger.Printf("Booting a VM failed with the following errors: %v\n", err)
		return false
	}
	defer server.Destroy()

	w.reporter.SendLog(fmt.Sprintf("Using worker: %s\n\n", w.Name))

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

	err = w.reporter.NotifyJobStarted()
	if err != nil {
		w.logger.Printf("Couldn't notify about job start: %v\n", err)
		return false
	}

	w.logger.Println("Running the build")
	exitCodeChan, err := w.runScript(ssh)
	if err != nil {
		w.logger.Printf("Failed to run build script: %v\n", err)
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
		case 2:
			w.reporter.NotifyJobFinished("errored")
		}
		return true
	case <-time.After(10 * time.Second):
		w.logger.Println("No log output after 10 seconds, stopping build")
		w.reporter.SendLog("\n\nYour build didn't produce any output in the last 10 seconds, stopping the job.\n")
		w.reporter.SendFinal()
		w.reporter.NotifyJobFinished("errored")
		return false
	}
}

func (w *Worker) jobID() int64 {
	return w.payload.Job.ID
}

func (w *Worker) bootServer() (VMCloudServer, error) {
	startTime := time.Now()
	hostname := fmt.Sprintf("testing-worker-go-%d-%s", os.Getpid(), w.Name)
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
	err := ssh.UploadFile("~/build.sh", []byte(fmt.Sprintf("echo This is build id %d", w.jobID())))
	if err != nil {
		return err
	}

	err = ssh.Run("chmod +x ~/build.sh")
	if err != nil {
		return err
	}

	return nil
}

func (w *Worker) runScript(ssh *SSHConnection) (<-chan int, error) {
	return ssh.Start("~/build.sh", w.reporter.Log)
}
