package worker

import (
	"time"

	"github.com/mitchellh/multistep"
)

type stepStartLogTimer struct {
	logTimeout time.Duration
}

func (s *stepStartLogTimer) Run(state multistep.StateBag) multistep.StepAction {
	logWriter := state.Get("logWriter").(LogWriter)
	logWriter.SetTimeout(s.logTimeout)

	return multistep.ActionContinue
}

func (s *stepStartLogTimer) Cleanup(state multistep.StateBag) {
}
