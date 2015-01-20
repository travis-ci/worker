package lib

import (
	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/lib/backend"
	"golang.org/x/net/context"
)

type stepRunScript struct{}

func (s *stepRunScript) Run(state multistep.StateBag) multistep.StepAction {
	ctx := state.Get("ctx").(context.Context)
	buildJob := state.Get("buildJob").(Job)

	instance := state.Get("instance").(backend.Instance)

	logWriter, err := buildJob.LogWriter(ctx)
	if err != nil {
		LoggerFromContext(ctx).WithField("err", err).Error("couldn't open a log writer")
		buildJob.Requeue()
		return multistep.ActionHalt
	}

	result, err := instance.RunScript(ctx, logWriter)
	if err != nil {
		LoggerFromContext(ctx).WithField("err", err).WithField("completed", result.Completed).Error("couldn't run script")

		if !result.Completed {
			buildJob.Requeue()
		}

		return multistep.ActionHalt
	}

	state.Put("scriptResult", result)

	return multistep.ActionContinue
}

func (s *stepRunScript) Cleanup(state multistep.StateBag) {
	// Nothing to clean up
}
