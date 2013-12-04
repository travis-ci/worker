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
	Name     string
	api      VMCloudAPI
	reporter *Reporter
	payload  Payload
	once     sync.Once
	logger   *log.Logger
}

func NewWorker(name string, api VMCloudAPI, payload Payload, reporter *Reporter) *Worker {
	return &Worker{
		Name:     name,
		api:      api,
		payload:  payload,
		logger:   log.New(os.Stdout, fmt.Sprintf("%s: ", name), log.Ldate|log.Ltime),
		reporter: reporter,
	}
}

func (w *Worker) Run() bool {
	server, err := w.bootServer()
	if err != nil {
		w.logger.Printf("Booting a VM failed with the following errors: %v\n", err)
		return false
	}
	defer server.Destroy()

	w.reporter.SendLog(w.jobId(), fmt.Sprintf("Using worker: %s\n\n", w.Name))

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

	err = w.reporter.NotifyJobStarted(w.jobId())
	if err != nil {
		w.logger.Printf("Couldn't notify about job start: %v\n", err)
		return false
	}

	w.logger.Println("Running the build")
	outputChan, exitCodeChan, err := w.runScript(ssh)
	if err != nil {
		w.logger.Printf("Failed to run build script: %v\n", err)
		return false
	}

	for {
		select {
		case bytes, ok := <-outputChan:
			if bytes != nil {
				err := w.reporter.SendLog(w.jobId(), string(bytes))
				if err != nil {
					w.logger.Printf("An error occurred while sending a log part: %v", err)
				}
			}
			if !ok {
				w.logger.Println("Build finished.")
				exitCode := <-exitCodeChan
				state := "errored"
				switch exitCode {
				case 0:
					state = "passed"
				case 1:
					state = "failed"
				}
				w.reporter.SendFinal(w.jobId())
				err := w.reporter.NotifyJobFinished(w.jobId(), state)
				if err != nil {
					w.logger.Printf("Couldn't send job:finished message: %v\n", err)
				}
				return true
			}
		case <-time.After(10 * time.Second):
			w.logger.Println("No log output after 10 seconds, stopping build")
			w.reporter.SendLog(w.jobId(), "\n\nYour build didn't produce any output in the last 10 seconds, stopping the job.\n")
			w.reporter.SendFinal(w.jobId())
			w.reporter.NotifyJobFinished(w.jobId(), "errored")
			return false
		}
	}
}

func (w *Worker) jobId() int64 {
	return w.payload.Job.Id
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
	err := ssh.UploadFile("~/build.sh", []byte(fmt.Sprintf("echo This is build id %d", w.jobId())))
	if err != nil {
		return err
	}

	err = ssh.Run("chmod +x ~/build.sh")
	if err != nil {
		return err
	}

	return nil
}

func (w *Worker) runScript(ssh *SSHConnection) (<-chan []byte, chan int, error) {
	return ssh.Start("~/build.sh")
}
