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
	Name       string
	api        VMCloudAPI
	jobChan    chan Job
	currentJob Job
	once       sync.Once
	logger     *log.Logger
}

type Job struct {
	Id          int
	BuildScript []byte
}

func NewWorker(name string, api VMCloudAPI, jobChan chan Job) *Worker {
	return &Worker{
		Name:    name,
		api:     api,
		jobChan: jobChan,
		logger:  log.New(os.Stdout, fmt.Sprintf("%s: ", name), log.Ldate|log.Ltime),
	}
}

func (w *Worker) Start() {
	w.once.Do(func() {
		w.logger.Println("Starting worker")
		for job := range w.jobChan {
			w.currentJob = job
			w.run()
		}
		w.logger.Println("Shutting down worker")
	})
}

func (w *Worker) run() {
	server, err := w.bootServer()
	if err != nil {
		w.logger.Printf("Booting a VM failed with the following errors: %v\n", err)
		return
	}
	defer server.Destroy()

	w.logger.Println("Opening SSH connection")
	ssh, err := NewSSHConnection(server)
	if err != nil {
		w.logger.Printf("Couldn't connect to SSH: %v\n", err)
		return
	}
	defer ssh.Close()

	w.logger.Println("Uploading build script")
	err = w.uploadScript(ssh)
	if err != nil {
		w.logger.Printf("Couldn't upload script to SSH: %v\n", err)
		return
	}

	w.logger.Println("Running the build")
	outputChan, err := w.runScript(ssh)
	if err != nil {
		w.logger.Printf("Failed to run build script: %v\n", err)
		return
	}

	for {
		select {
		case bytes, ok := <-outputChan:
			if !ok {
				w.logger.Println("Build finished.")
				return
			}
			if bytes != nil {
				fmt.Printf("%s", bytes)
			}
		case <-time.After(10 * time.Second):
			w.logger.Println("No log output after 10 seconds, stopping build")
			return
		}
	}
}

func (w *Worker) bootServer() (VMCloudServer, error) {
	startTime := time.Now()
	hostname := fmt.Sprintf("testing-worker-go-%d-%s-%d", os.Getpid(), w.Name, w.currentJob.Id)
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
	err := ssh.UploadFile("~/build.sh", w.currentJob.BuildScript)
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
