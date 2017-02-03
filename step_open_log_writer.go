package worker

import (
	"time"

	gocontext "golang.org/x/net/context"

	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/context"
)

type stepOpenLogWriter struct {
	maxLogLength      int
	defaultLogTimeout time.Duration
}

func (s *stepOpenLogWriter) Run(state multistep.StateBag) multistep.StepAction {
	ctx := state.Get("ctx").(gocontext.Context)
	buildJob := state.Get("buildJob").(Job)

	logWriter, err := buildJob.LogWriter(ctx, s.defaultLogTimeout)
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't open a log writer")
		context.CaptureError(ctx, err)

		err := buildJob.Requeue(ctx)
		if err != nil {
			context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't requeue job")
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
