package worker

import (
	"time"

	"github.com/cenk/backoff"
	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/context"
	gocontext "golang.org/x/net/context"
)

type stepGenerateScript struct {
	generator BuildScriptGenerator
}

func (s *stepGenerateScript) Run(state multistep.StateBag) multistep.StepAction {
	buildJob := state.Get("buildJob").(Job)
	ctx := state.Get("ctx").(gocontext.Context)

	b := backoff.NewExponentialBackOff()
	b.MaxInterval = 10 * time.Second
	b.MaxElapsedTime = time.Minute

	var script []byte
	var err error
	switch job := buildJob.(type) {
	case BuildScriptGenerator:
		script, err = job.Generate(ctx, buildJob)
	default:
		err = backoff.Retry(func() (err error) {
			script, err = s.generator.Generate(ctx, buildJob)
			return
		}, b)
	}

	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't generate build script, erroring job")
		err := buildJob.Error(ctx, "An error occurred while generating the build script.")
		if err != nil {
			context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't requeue job")
		}

		return multistep.ActionHalt
	}

	context.LoggerFromContext(ctx).Info("generated script")

	state.Put("script", script)

	return multistep.ActionContinue
}

func (s *stepGenerateScript) Cleanup(multistep.StateBag) {
	// Nothing to clean up
}
