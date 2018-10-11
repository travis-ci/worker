package worker

import (
	gocontext "context"
	"time"

	"github.com/mitchellh/multistep"
	"go.opencensus.io/trace"
)

type stepSleep struct {
	duration time.Duration
}

func (s *stepSleep) Run(state multistep.StateBag) multistep.StepAction {
	ctx := state.Get("ctx").(gocontext.Context)

	ctx, span := trace.StartSpan(ctx, "Sleep.Run")
	defer span.End()

	time.Sleep(s.duration)

	return multistep.ActionContinue
}

func (s *stepSleep) Cleanup(state multistep.StateBag) {}
