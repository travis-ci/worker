package worker

import "github.com/mitchellh/multistep"

type stepSubscribeCancellation struct {
	cancellationBroadcaster *CancellationBroadcaster
}

func (s *stepSubscribeCancellation) Run(state multistep.StateBag) multistep.StepAction {
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

	buildJob := state.Get("buildJob").(Job)
	ch := state.Get("cancelChan").(<-chan struct{})
	s.cancellationBroadcaster.Unsubscribe(buildJob.Payload().Job.ID, ch)
}
