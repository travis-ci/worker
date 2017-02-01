package worker

import (
	gocontext "context"
	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/context"
)

type stepCheckCancellation struct{}

func (s *stepCheckCancellation) Run(state multistep.StateBag) multistep.StepAction {
	ctx := state.Get("ctx").(gocontext.Context)
	buildJob := state.Get("buildJob").(Job)
	logWriter := state.Get("logWriter").(LogWriter)
	cancelChan := state.Get("cancelChan").(<-chan struct{})

	select {
	case <-cancelChan:
		s.writeLogAndFinishWithState(ctx, logWriter, buildJob, FinishStateCancelled, "\n\nDone: Job Cancelled\n\n")
	default:
	}

	return multistep.ActionHalt
}

func (s *stepCheckCancellation) Cleanup(state multistep.StateBag) {
}

func (s *stepCheckCancellation) writeLogAndFinishWithState(ctx gocontext.Context, logWriter LogWriter, buildJob Job, state FinishState, logMessage string) {
	_, err := logWriter.WriteAndClose([]byte(logMessage))
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't write final log message")
	}

	err = buildJob.Finish(state)
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).WithField("state", state).Error("couldn't update job state")
	}
}
