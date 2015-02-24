package lib

import (
	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/lib/context"
	gocontext "golang.org/x/net/context"
)

type stepSendReceived struct{}

func (s *stepSendReceived) Run(state multistep.StateBag) multistep.StepAction {
	buildJob := state.Get("buildJob").(Job)
	ctx := state.Get("ctx").(gocontext.Context)

	err := buildJob.Received()
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't send received event")
	}

	return multistep.ActionContinue
}

func (s *stepSendReceived) Cleanup(state multistep.StateBag) {
}
