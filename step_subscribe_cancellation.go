package worker

import (
	gocontext "context"

	"github.com/mitchellh/multistep"
	"go.opencensus.io/trace"
)

type stepSubscribeCancellation struct {
	cancellationBroadcaster *CancellationBroadcaster
}

func (s *stepSubscribeCancellation) Run(state multistep.StateBag) multistep.StepAction {
	ctx := state.Get("ctx").(gocontext.Context)

	ctx, span := trace.StartSpan(ctx, "SubscribeCancellation.Run")
	defer span.End()

	if s.cancellationBroadcaster == nil {
		ch := make(chan struct{})
		state.Put("cancelChan", (<-chan struct{})(ch))
		return multistep.ActionContinue
	}

	buildJob := state.Get("buildJob").(Job)
	ch := s.cancellationBroadcaster.Subscribe(buildJob.Payload().Job.ID)
	state.Put("cancelChan", ch)

	return multistep.ActionContinue
}

func (s *stepSubscribeCancellation) Cleanup(state multistep.StateBag) {
	if s.cancellationBroadcaster == nil {
		return
	}

	ctx := state.Get("ctx").(gocontext.Context)

	ctx, span := trace.StartSpan(ctx, "SubscribeCancellation.Cleanup")
	defer span.End()

	buildJob := state.Get("buildJob").(Job)
	ch := state.Get("cancelChan").(<-chan struct{})
	s.cancellationBroadcaster.Unsubscribe(buildJob.Payload().Job.ID, ch)
}
