package lib

import (
	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/lib/backend"
)

type stepUpdateState struct{}

func (s *stepUpdateState) Run(state multistep.StateBag) multistep.StepAction {
	buildJob := state.Get("buildJob").(Job)

	result := state.Get("scriptResult").(backend.RunResult)

	switch result.ExitCode {
	case 0:
		buildJob.Finish(FinishStatePassed)
	case 1:
		buildJob.Finish(FinishStateFailed)
	default:
		buildJob.Finish(FinishStateErrored)
	}

	return multistep.ActionContinue
}

func (s *stepUpdateState) Cleanup(state multistep.StateBag) {
	// Nothing to clean up
}
