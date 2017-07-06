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
	ctx := state.Get("ctx").(gocontext.Context)

	err := buildJob.Started()
	if err != nil {
		context.LoggerFromContext(ctx).WithFields(logrus.Fields{
			"err":  err,
			"self": "step_update_state",
		}).Error("couldn't mark job as started")
	}

	return multistep.ActionContinue
}

func (s *stepUpdateState) Cleanup(state multistep.StateBag) {
	buildJob := state.Get("buildJob").(Job)
	ctx := state.Get("ctx").(gocontext.Context)

	mresult, ok := state.GetOk("scriptResult")

	if ok {
		result := mresult.(*backend.RunResult)

		var err error

		switch result.ExitCode {
		case 0:
			err = buildJob.Finish(ctx, FinishStatePassed)
		case 1:
			err = buildJob.Finish(ctx, FinishStateFailed)
		default:
			err = buildJob.Finish(ctx, FinishStateErrored)
		}

		if err != nil {
			context.LoggerFromContext(ctx).WithFields(logrus.Fields{
				"err":  err,
				"self": "step_update_state",
			}).Error("couldn't mark job as finished")
		}
	}
}
