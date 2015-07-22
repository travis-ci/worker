package worker

import (
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

	script, err := s.generator.Generate(ctx, buildJob.RawPayload())
	if err != nil {
		if genErr, ok := err.(BuildScriptGeneratorError); ok && !genErr.Recover {
			context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't generate build script, erroring job")
			err := buildJob.Error(ctx, "An error occurred while generating the build script.")
			if err != nil {
				context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't error job")
			}

			return multistep.ActionHalt
		}

		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't generate build script, requeueing job")
		err := buildJob.Requeue()
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
