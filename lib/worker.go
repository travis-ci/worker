package lib

import (
	"fmt"
	"os"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/travis-ci/worker/lib/ssh"
)

// A Worker runs a job.
type Worker struct {
	Name         string
	Cancel       chan bool
	vmProvider   VMProvider
	mb           MessageBroker
	logger       *logrus.Logger
	payload      Payload
	stateUpdater *StateUpdater
	jobLog       *LogWriter
	tw           *TimeoutWriter
	lw           *LimitWriter
	timeouts     TimeoutsConfig
	logLimits    LogLimitsConfig
	dispatcher   *Dispatcher
	metrics      Metrics
	config       WorkerConfig
}

// NewWorker returns a new worker that can process a single job payload.
func NewWorker(mb MessageBroker, dispatcher *Dispatcher, metrics Metrics, logger *logrus.Logger, config WorkerConfig) *Worker {
	var provider VMProvider
	switch config.Provider {
	case "blueBox":
		provider = NewBlueBox(config.BlueBox)
	case "sauceLabs":
		provider = NewSauceLabs(config.SauceLabs)
	default:
		logger.WithField("provider", config.Provider).Error("NewWorker: unknown provider")
		return nil
	}

	return &Worker{
		mb:         mb,
		logger:     logger,
		vmProvider: provider,
		Name:       config.Name,
		Cancel:     make(chan bool, 1),
		timeouts:   config.Timeouts,
		logLimits:  config.LogLimits,
		dispatcher: dispatcher,
		metrics:    metrics,
		config:     config,
	}
}

// Process actually runs the job.
func (w *Worker) Process(payload Payload) {
	w.payload = payload
	log := w.logger.WithFields(logrus.Fields{
		"slug":   w.payload.Repository.Slug,
		"job_id": w.jobID(),
	})
	log.Info("starting the job")
	defer log.Info("job finished")

	w.dispatcher.Register(w, w.jobID())
	defer w.dispatcher.Deregister(w.jobID())

	w.stateUpdater = NewStateUpdater(w.mb, w.jobID())
	w.jobLog = NewLogWriter(w.mb, w.jobID())

	var err error
	server, err := w.bootServer()
	if err != nil {
		log.WithField("err", err).Error("booting a VM failed")
		w.vmCreationError()
		return
	}
	defer server.Destroy()
	defer log.Info("destroying the VM")

	defer w.jobLog.Close()

	select {
	case <-w.Cancel:
		w.markJobAsCancelled()
		return
	default:
	}

	log.Info("opening an SSH connection")
	sshConn, err := w.openSSHConn(server)
	if err != nil {
		log.WithField("err", err).Error("couldn't connect to SSH")
		w.connectionError()
		return
	}
	defer sshConn.Close()
	defer log.Info("closing the SSH connection")

	log.Info("uploading the build.sh script")
	err = w.uploadScript(sshConn)
	if err != nil {
		log.WithField("err", err).Error("couldn't upload script")
		w.connectionError()
		return
	}
	defer w.removeScript(sshConn)

	err = w.stateUpdater.Start()
	if err != nil {
		log.WithField("err", err).Error("couldn't notify about job starting")
		return
	}

	log.Info("running the job")
	exitCodeChan, err := w.runScript(sshConn)
	if err != nil {
		log.WithField("err", err).Error("failed to run build script")
		w.connectionError()
		return
	}

	select {
	case exitCode := <-exitCodeChan:
		switch exitCode {
		case 0:
			w.finishWithState("passed")
		case 1:
			w.finishWithState("failed")
		default:
			w.finishWithState("errored")
		case -1:
			w.connectionError()
		}
		return
	case <-w.Cancel:
		w.markJobAsCancelled()
		return
	case <-w.tw.Timeout:
		log.Info("job timed out due to log inactivity")
		fmt.Fprintf(w.jobLog, noLogOutputMessage, w.timeouts.LogInactivity/60)
		return
	case <-w.lw.LimitReached:
		log.Info("job stopped due to log limit being reached")
		fmt.Fprintf(w.jobLog, logTooLongMessage, w.logLimits.MaxLogLength/1024/1024)
		return
	case <-time.After(time.Duration(w.timeouts.HardLimit) * time.Second):
		log.Info("job timed out due to hard timeout")
		fmt.Fprintf(w.jobLog, stalledBuildMessage, w.timeouts.HardLimit/60)
		w.finishWithState("errored")
		return
	}
}

func (w *Worker) jobID() int64 {
	return w.payload.Job.ID
}

func (w *Worker) bootServer() (VM, error) {
	startTime := time.Now()
	hostname := fmt.Sprintf("testing-%s-pid-%d-job-%d", w.Name, os.Getpid(), w.jobID())
	log := w.logger.WithFields(logrus.Fields{
		"slug":   w.payload.Repository.Slug,
		"job_id": w.jobID(),
	})

	log.WithField("hostname", hostname).Info("booting VM with hostname")

	server, err := w.vmProvider.Start(hostname, w.payload.Config["language"].(string), time.Duration(w.timeouts.VMBoot)*time.Second)
	if err != nil {
		switch err.(type) {
		case BootTimeoutError:
			w.metrics.MarkBootTimeout(w.metricsProvider())
		default:
			w.metrics.MarkBootError(w.metricsProvider())
		}
		return nil, err
	}

	bootDuration := time.Now().Sub(startTime)
	log.WithField("seconds", bootDuration.Seconds()).Info("VM provisioned")
	w.metrics.BootTimer(w.metricsProvider(), bootDuration)

	return server, nil
}

func (w *Worker) uploadScript(ssh *ssh.Connection) error {
	err := ssh.Run("test ! -f ~/build.sh")
	if err != nil {
		return err
	}

	script, err := NewBuildScriptGenerator(w.config.BuildAPI.Endpoint, w.config.BuildAPI.ApiKey).GenerateForPayload(w.payload)
	if err != nil {
		return err
	}

	err = ssh.UploadFile("~/build.sh", script)
	if err != nil {
		return err
	}

	return ssh.Run("chmod +x ~/build.sh")
}

func (w *Worker) removeScript(ssh *ssh.Connection) error {
	return ssh.Run("rm ~/build.sh")
}

func (w *Worker) runScript(ssh *ssh.Connection) (<-chan int, error) {
	fmt.Fprintf(w.jobLog, "Using: %s\n\n", w.Name)
	cw := NewCoalesceWriteCloser(w.jobLog)
	w.tw = NewTimeoutWriter(cw, time.Duration(w.timeouts.LogInactivity)*time.Second)
	w.lw = NewLimitWriter(w.tw, w.logLimits.MaxLogLength)
	return ssh.Start("~/build.sh", w.lw)
}

func (w *Worker) vmCreationError() {
	fmt.Fprintf(w.jobLog, vmCreationErrorMessage)
	w.logger.WithFields(logrus.Fields{
		"slug":   w.payload.Repository.Slug,
		"job_id": w.jobID(),
	}).Info("requeuing job due to VM creation error")
	w.requeueJob()
}

func (w *Worker) connectionError() {
	fmt.Fprintf(w.jobLog, connectionErrorMessage)
	w.logger.WithFields(logrus.Fields{
		"slug":   w.payload.Repository.Slug,
		"job_id": w.jobID(),
	}).Info("requeuing job due to SSH connection error")
	w.requeueJob()
}

func (w *Worker) requeueJob() {
	w.metrics.MarkJobRequeued()
	w.stateUpdater.Reset()
}

func (w *Worker) finishWithState(state string) {
	w.logger.WithFields(logrus.Fields{
		"slug":   w.payload.Repository.Slug,
		"job_id": w.jobID(),
	}).Info("job completed with state:%s", state)
	w.stateUpdater.Finish(state)
}

func (w *Worker) markJobAsCancelled() {
	w.logger.WithFields(logrus.Fields{
		"slug":   w.payload.Repository.Slug,
		"job_id": w.jobID(),
	}).Info("cancelling job")
	fmt.Fprint(w.jobLog, cancelledJobMessage)
	w.finishWithState("canceled")
}

func (w *Worker) metricsProvider() string {
	switch w.config.Provider {
	case "blueBox":
		return "bluebox"
	case "sauceLabs":
		return "saucelabs"
	}
	return ""
}

func (w *Worker) openSSHConn(server VM) (*ssh.Connection, error) {
	sshInfo := server.SSHInfo()

	var auths []ssh.AuthenticationMethod
	if sshInfo.SSHKeyPath != "" {
		keyAuth, err := ssh.SSHKeyAuthentication(sshInfo.SSHKeyPath, sshInfo.SSHKeyPassphrase)
		if err != nil {
			return nil, err
		}

		auths = append(auths, keyAuth)
	}
	if sshInfo.Password != "" {
		auths = append(auths, ssh.PasswordAuthentication(sshInfo.Password))
	}

	return ssh.NewConnection(sshInfo.Addr, sshInfo.Username, auths)
}
