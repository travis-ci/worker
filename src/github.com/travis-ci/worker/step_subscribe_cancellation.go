package worker

import (
	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/context"
	gocontext "golang.org/x/net/context"
)

type stepSubscribeCancellation struct {
	canceller Canceller
}

func (s *stepSubscribeCancellation) Run(state multistep.StateBag) multistep.StepAction {
	ctx := state.Get("ctx").(gocontext.Context)
	buildJob := state.Get("buildJob").(Job)

	ch := make(chan struct{})
	state.Put("cancelChan", (<-chan struct{})(ch))
	err := s.canceller.Subscribe(buildJob.Payload().Job.ID, ch)
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't subscribe to canceller")
		err := buildJob.Requeue()
		if err != nil {
			context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't requeue job")
		}
		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (s *stepSubscribeCancellation) Cleanup(state multistep.StateBag) {
	buildJob := state.Get("buildJob").(Job)
	s.canceller.Unsubscribe(buildJob.Payload().Job.ID)
}
