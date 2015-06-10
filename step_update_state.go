package worker

import (
	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/backend"
)

type stepUpdateState struct{}

func (s *stepUpdateState) Run(state multistep.StateBag) multistep.StepAction {
	buildJob := state.Get("buildJob").(Job)

	buildJob.Started()

	return multistep.ActionContinue
}

func (s *stepUpdateState) Cleanup(state multistep.StateBag) {
	buildJob := state.Get("buildJob").(Job)

	mresult, ok := state.GetOk("scriptResult")

	if ok {
		result := mresult.(*backend.RunResult)

		switch result.ExitCode {
		case 0:
			buildJob.Finish(FinishStatePassed)
		case 1:
			buildJob.Finish(FinishStateFailed)
		default:
			buildJob.Finish(FinishStateErrored)
		}
	}
}
