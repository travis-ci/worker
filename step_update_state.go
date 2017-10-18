package worker

import (
	gocontext "context"

	"github.com/mitchellh/multistep"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
)

type stepUpdateState struct{}

func (s *stepUpdateState) Run(state multistep.StateBag) multistep.StepAction {
	buildJob := state.Get("buildJob").(Job)
	instance := state.Get("instance").(backend.Instance)
	instanceID := instance.ID()

	ctx := state.Get("ctx").(gocontext.Context)
	if instanceID != "" {
		ctx = context.FromInstanceID(ctx, instanceID)
		state.Put("ctx", ctx)
	}

	err := buildJob.Started(ctx)
	if err != nil {
		context.LoggerFromContext(ctx).WithFields(logrus.Fields{
			"err":         err,
			"self":        "step_update_state",
			"instance_id": instanceID,
		}).Error("couldn't mark job as started")
	}

	return multistep.ActionContinue
}

func (s *stepUpdateState) Cleanup(state multistep.StateBag) {
	buildJob := state.Get("buildJob").(Job)
	procCtx := state.Get("procCtx").(gocontext.Context)
	ctx := state.Get("ctx").(gocontext.Context)

	mresult, ok := state.GetOk("scriptResult")

	if ok {
		result := mresult.(*backend.RunResult)

		var err error

		switch result.ExitCode {
		case 0:
			err = buildJob.Finish(procCtx, FinishStatePassed)
		case 1:
			err = buildJob.Finish(procCtx, FinishStateFailed)
		default:
			err = buildJob.Finish(procCtx, FinishStateErrored)
		}

		if err != nil {
			context.LoggerFromContext(ctx).WithFields(logrus.Fields{
				"err":  err,
				"self": "step_update_state",
			}).Error("couldn't mark job as finished")
		}
	}
}
