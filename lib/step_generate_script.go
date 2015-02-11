package lib

import (
	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/lib/context"
	gocontext "golang.org/x/net/context"
)

type stepGenerateScript struct {
	generator BuildScriptGenerator
}

func (s *stepGenerateScript) Run(state multistep.StateBag) multistep.StepAction {
	buildJob := state.Get("buildJob").(Job)
	ctx := state.Get("ctx").(gocontext.Context)

	script, err := s.generator.Generate(ctx, buildJob.Payload())
	if err != nil {
		if genErr, ok := err.(BuildScriptGeneratorError); ok && genErr.Recover {
			context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't generate build script, requeueing job")
			buildJob.Requeue()
			return multistep.ActionHalt
		}

		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't generate build script, erroring job")
		buildJob.Error(ctx, "An error occurred while generating the build script.")
		return multistep.ActionHalt
	}

	state.Put("script", script)

	return multistep.ActionContinue
}

func (s *stepGenerateScript) Cleanup(multistep.StateBag) {
	// Nothing to clean up
}
