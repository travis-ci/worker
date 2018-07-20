package worker

import (
	"time"

	gocontext "context"

	"github.com/mitchellh/multistep"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/context"
)

type stepOpenLogWriter struct {
	maxLogLength      int
	defaultLogTimeout time.Duration
}

func (s *stepOpenLogWriter) Run(state multistep.StateBag) multistep.StepAction {
	procCtx := state.Get("procCtx").(gocontext.Context)
	ctx := state.Get("ctx").(gocontext.Context)
	buildJob := state.Get("buildJob").(Job)
	logsQueue := state.Get("logsQueue").(LogsQueue)
	logger := context.LoggerFromContext(ctx).WithField("self", "step_open_log_writer")

	if logsQueue {
		logWriter, err := logsQueue.LogWriter(ctx, s.defaultLogTimeout, buildJob)
	} else {
		logWriter, err := buildJob.LogWriter(ctx, s.defaultLogTimeout)
	}
	if err != nil {
		logger.WithFields(logrus.Fields{
			"err":         err,
			"log_timeout": s.defaultLogTimeout,
		}).Error("couldn't open a log writer, attempting requeue")
		context.CaptureError(ctx, err)

		err := buildJob.Requeue(procCtx)
		if err != nil {
			logger.WithField("err", err).Error("couldn't requeue job")
		}
		return multistep.ActionHalt
	}
	logWriter.SetMaxLogLength(s.maxLogLength)

	state.Put("logWriter", logWriter)

	return multistep.ActionContinue
}

func (s *stepOpenLogWriter) Cleanup(state multistep.StateBag) {
	logWriter, ok := state.Get("logWriter").(LogWriter)
	if ok {
		logWriter.Close()
	}
}
