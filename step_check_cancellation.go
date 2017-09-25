package worker

import (
	gocontext "context"

	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/context"
)

type stepCheckCancellation struct{}

func (s *stepCheckCancellation) Run(state multistep.StateBag) multistep.StepAction {
	cancelChan := state.Get("cancelChan").(<-chan struct{})

	select {
	case <-cancelChan:
		procCtx := state.Get("procCtx").(gocontext.Context)
		ctx := state.Get("ctx").(gocontext.Context)
		buildJob := state.Get("buildJob").(Job)
		if _, ok := state.GetOk("logWriter"); ok {
			logWriter := state.Get("logWriter").(LogWriter)
			s.writeLogAndFinishWithState(procCtx, ctx, logWriter, buildJob, FinishStateCancelled, "\n\nDone: Job Cancelled\n\n")
		} else {
			err := buildJob.Finish(procCtx, FinishStateCancelled)
			if err != nil {
				context.LoggerFromContext(ctx).WithField("err", err).WithField("state", state).Error("couldn't update job state")
			}
		}
		return multistep.ActionHalt
	default:
	}

	return multistep.ActionContinue
}

func (s *stepCheckCancellation) Cleanup(state multistep.StateBag) {}

func (s *stepCheckCancellation) writeLogAndFinishWithState(procCtx, ctx gocontext.Context, logWriter LogWriter, buildJob Job, state FinishState, logMessage string) {
	_, err := logWriter.WriteAndClose([]byte(logMessage))
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't write final log message")
	}

	err = buildJob.Finish(procCtx, state)
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).WithField("state", state).Error("couldn't update job state")
	}
}
