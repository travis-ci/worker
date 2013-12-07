package main

import (
	"fmt"
	"log"
	"os"
	"time"
)

// A Worker runs a job.
type Worker struct {
	Name       string
	vmProvider VMProvider
	mb         MessageBroker
	logger     *log.Logger
	payload    Payload
	reporter   *Reporter
	tw         *TimeoutWriter
	lw         *LimitWriter
	timeouts   TimeoutsConfig
	logLimits  LogLimitsConfig
}

// NewWorker creates a new worker with the given parameters. The worker assumes
// ownership of the given API, payload and reporter and they should not be
// reused for other workers.
func NewWorker(name string, VMProvider VMProvider, mb MessageBroker, timeouts TimeoutsConfig, logLimits LogLimitsConfig) *Worker {
	return &Worker{
		Name:       name,
		vmProvider: VMProvider,
		mb:         mb,
		logger:     log.New(os.Stdout, fmt.Sprintf("%s: ", name), log.Ldate|log.Ltime),
		timeouts:   timeouts,
		logLimits:  logLimits,
	}
}

// Process actually runs the job. It returns an error if an error occurred that
// should cause the job to be requeued.
func (w *Worker) Process(payload Payload) error {
	defer w.logger.Println("Finishing job")

	var err error
	w.payload = payload
	w.reporter, err = NewReporter(w.mb, payload.Job.ID)
	if err != nil {
		return err
	}

	w.logger.Printf("Starting job slug:%s id:%d\n", w.payload.Repository.Slug, w.payload.Job.ID)

	server, err := w.bootServer()
	if err != nil {
		w.logger.Printf("Booting a VM failed with the following errors: %v\n", err)
		return err
	}
	defer server.Destroy()

	fmt.Fprintf(w.reporter.Log, "Using worker: %s\n\n", w.Name)
	defer w.reporter.Log.Close()

	w.logger.Println("Opening SSH connection")
	ssh, err := NewSSHConnection(server, w.Name)
	if err != nil {
		w.logger.Printf("Couldn't connect to SSH: %v\n", err)
		fmt.Fprintf(w.reporter.Log, connectionErrorMessage)
		return err
	}
	defer ssh.Close()

	w.logger.Println("Uploading build script")
	err = w.uploadScript(ssh)
	if err != nil {
		w.logger.Printf("Couldn't upload script to SSH: %v\n", err)
		fmt.Fprintf(w.reporter.Log, connectionErrorMessage)
		return err
	}

	err = w.reporter.NotifyJobStarted()
	if err != nil {
		w.logger.Printf("Couldn't notify about job start: %v\n", err)
		return err
	}

	w.logger.Println("Running the build")
	exitCodeChan, err := w.runScript(ssh)
	if err != nil {
		w.logger.Printf("Failed to run build script: %v\n", err)
		fmt.Fprintf(w.reporter.Log, connectionErrorMessage)
		return err
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
			fmt.Fprintf(w.reporter.Log, connectionErrorMessage)
			return fmt.Errorf("an error occurred with the SSH connection")
		}
		return nil
	case <-w.tw.Timeout:
		fmt.Fprintf(w.reporter.Log, noLogOutputMessage, w.timeouts.LogInactivity/60)
		return nil
	case <-w.lw.LimitReached:
		fmt.Fprintf(w.reporter.Log, logTooLongMessage, w.logLimits.MaxLogLength/1024/1024)
		return nil
	case <-time.After(time.Duration(w.timeouts.HardLimit) * time.Second):
		fmt.Fprintf(w.reporter.Log, stalledBuildMessage, w.timeouts.HardLimit/60)
		w.reporter.NotifyJobFinished("errored")
		return nil
	}
}

func (w *Worker) jobID() int64 {
	return w.payload.Job.ID
}

func (w *Worker) bootServer() (VM, error) {
	startTime := time.Now()
	hostname := fmt.Sprintf("testing-worker-go-%d-%s-%d", os.Getpid(), w.Name, w.jobID())
	w.logger.Printf("Booting %s\n", hostname)
	server, err := w.vmProvider.Start(hostname, time.Duration(w.timeouts.VMBoot)*time.Second)
	if err != nil {
		return nil, err
	}

	w.logger.Printf("VM provisioned in %.2f seconds\n", time.Now().Sub(startTime).Seconds())

	return server, nil
}

func (w *Worker) uploadScript(ssh *SSHConnection) error {
	err := ssh.UploadFile("~/build.sh", []byte(fmt.Sprintf("#!/bin/bash --login\n\necho This is build id %d\nfor i in {1..200}; do echo -n \"$i \"; sleep 1; done", w.jobID())))
	if err != nil {
		return err
	}

	return ssh.Run("chmod +x ~/build.sh")
}

func (w *Worker) runScript(ssh *SSHConnection) (<-chan int, error) {
	w.tw = NewTimeoutWriter(w.reporter.Log, time.Duration(w.timeouts.LogInactivity)*time.Second)
	w.lw = NewLimitWriter(w.tw, w.logLimits.MaxLogLength)
	return ssh.Start("~/build.sh", w.lw)
}
