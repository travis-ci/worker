package worker

import (
	"time"

	"github.com/mitchellh/multistep"
)

type stepStartLogTimer struct {
	logTimeout   time.Duration
	maxLogLength int
}

func (s *stepStartLogTimer) Run(state multistep.StateBag) multistep.StepAction {
	logWriter := state.Get("logWriter").(LogWriter)
	logWriter.SetTimeout(s.logTimeout)
	logWriter.SetMaxLogLength(s.maxLogLength)

	return multistep.ActionContinue
}

func (s *stepStartLogTimer) Cleanup(state multistep.StateBag) {
}
