package lib

import (
	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/lib/backend"
	"golang.org/x/net/context"
)

type stepUploadScript struct{}

func (s *stepUploadScript) Run(state multistep.StateBag) multistep.StepAction {
	ctx := state.Get("ctx").(context.Context)
	buildJob := state.Get("buildJob").(Job)

	instance := state.Get("instance").(backend.Instance)
	script := state.Get("script").([]byte)

	err := instance.UploadScript(ctx, script)
	if err != nil {
		LoggerFromContext(ctx).WithField("err", err).Error("couldn't upload script")
		buildJob.Requeue()

		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (s *stepUploadScript) Cleanup(state multistep.StateBag) {
	// Nothing to clean up
}
