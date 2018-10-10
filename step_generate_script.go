package worker

import (
	"time"

	gocontext "context"

	"github.com/cenk/backoff"
	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/context"
	"go.opencensus.io/trace"
)

type stepGenerateScript struct {
	generator BuildScriptGenerator
}

func (s *stepGenerateScript) Run(state multistep.StateBag) multistep.StepAction {
	procCtx := state.Get("procCtx").(gocontext.Context)
	buildJob := state.Get("buildJob").(Job)
	ctx := state.Get("ctx").(gocontext.Context)

	ctx, span := trace.StartSpan(ctx, "stepGenerateScript")
	defer span.End()

	logger := context.LoggerFromContext(ctx).WithField("self", "step_generate_script")

	b := backoff.NewExponentialBackOff()
	b.MaxInterval = 10 * time.Second
	b.MaxElapsedTime = time.Minute

	var script []byte
	var err error
	switch job := buildJob.(type) {
	case BuildScriptGenerator:
		logger.Info("using job to get script")
		script, err = job.Generate(ctx, buildJob)
	default:
		logger.Info("using build script generator to generate script")
		err = backoff.Retry(func() (err error) {
			script, err = s.generator.Generate(ctx, buildJob)
			return
		}, b)
	}

	if err != nil {
		logger.WithField("err", err).Error("couldn't generate build script, erroring job")
		err := buildJob.Error(procCtx, "An error occurred while generating the build script.")
		if err != nil {
			logger.WithField("err", err).Error("couldn't requeue job")
		}

		return multistep.ActionHalt
	}

	logger.Info("generated script")

	state.Put("script", script)

	return multistep.ActionContinue
}

func (s *stepGenerateScript) Cleanup(multistep.StateBag) {
	// Nothing to clean up
}
