package worker

type stepSleep struct {
	duration time.Duration
}

func (s *stepSleep) Run(state multistep.StateBag) multistep.StepAction {
	time.Sleep(s.duration)

	return multistep.ActionContinue
}

func (s *stepSleep) Cleanup(state multistep.StateBag) {
}
