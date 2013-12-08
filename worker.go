package main

import (
	"fmt"
	"os"
	"time"
)

// A Worker runs a job.
type Worker struct {
	Name       string
	vmProvider VMProvider
	mb         MessageBroker
	logger     *Logger
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
func NewWorker(name string, VMProvider VMProvider, mb MessageBroker, logger *Logger, timeouts TimeoutsConfig, logLimits LogLimitsConfig) *Worker {
	return &Worker{
		Name:       name,
		vmProvider: VMProvider,
		mb:         mb,
		timeouts:   timeouts,
		logLimits:  logLimits,
		logger:     logger,
	}
}

// Process actually runs the job. It returns an error if an error occurred that
// should cause the job to be requeued.
func (w *Worker) Process(payload Payload) error {
	w.payload = payload
	w.logger = w.logger.Set("slug", w.payload.Repository.Slug).Set("job_id", w.jobID())
	w.logger.Info("starting job")
	defer w.logger.Info("finishing job")

	var err error
	w.reporter, err = NewReporter(w.mb, w.jobID())
	if err != nil {
		return err
	}

	server, err := w.bootServer()
	if err != nil {
		w.logger.Errorf("booting a VM failed with the following error: %v", err)
		return err
	}
	defer server.Destroy()

	fmt.Fprintf(w.reporter.Log, "Using worker: %s\n\n", w.Name)
	defer w.reporter.Log.Close()

	w.logger.Info("opening SSH connection")
	ssh, err := NewSSHConnection(server, w.Name)
	if err != nil {
		w.logger.Errorf("couldn't connect to SSH: %v", err)
		w.connectionError()
		return err
	}
	defer ssh.Close()

	w.logger.Info("uploading build script")
	err = w.uploadScript(ssh)
	if err != nil {
		w.logger.Errorf("couldn't upload script: %v")
		w.connectionError()
		return err
	}

	err = w.reporter.NotifyJobStarted()
	if err != nil {
		w.logger.Errorf("couldn't notify about job starting: %v", err)
		return err
	}

	w.logger.Info("running the job")
	exitCodeChan, err := w.runScript(ssh)
	if err != nil {
		w.logger.Errorf("failed to run build script: %v", err)
		w.connectionError()
		return err
	}

	select {
	case exitCode := <-exitCodeChan:
		w.logger.Info("job finished")
		switch exitCode {
		case 0:
			w.finishWithState("passed")
		case 1:
			w.finishWithState("failed")
		default:
			w.finishWithState("errored")
		case -1:
			w.connectionError()
			return fmt.Errorf("an error occurred with the SSH connection")
		}
		return nil
	case <-w.tw.Timeout:
		w.logger.Info("job timed out due to log inactivity")
		fmt.Fprintf(w.reporter.Log, noLogOutputMessage, w.timeouts.LogInactivity/60)
		return nil
	case <-w.lw.LimitReached:
		w.logger.Info("job stopped due to log limit being reached")
		fmt.Fprintf(w.reporter.Log, logTooLongMessage, w.logLimits.MaxLogLength/1024/1024)
		return nil
	case <-time.After(time.Duration(w.timeouts.HardLimit) * time.Second):
		w.logger.Info("job timed out due to hard timeout")
		fmt.Fprintf(w.reporter.Log, stalledBuildMessage, w.timeouts.HardLimit/60)
		w.finishWithState("errored")
		return nil
	}
}

func (w *Worker) jobID() int64 {
	return w.payload.Job.ID
}

func (w *Worker) bootServer() (VM, error) {
	startTime := time.Now()
	hostname := fmt.Sprintf("testing-%s-pid-%d-job-%d", w.Name, os.Getpid(), w.jobID())
	w.logger.Infof("booting %s", hostname)
	server, err := w.vmProvider.Start(hostname, time.Duration(w.timeouts.VMBoot)*time.Second)
	if err != nil {
		return nil, err
	}

	w.logger.Infof("VM provisioned in %.2f seconds", time.Now().Sub(startTime).Seconds())

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

func (w *Worker) connectionError() {
	fmt.Fprintf(w.reporter.Log, connectionErrorMessage)
	w.finishWithState("errored")
}

func (w *Worker) finishWithState(state string) {
	w.reporter.NotifyJobFinished(state)
}
