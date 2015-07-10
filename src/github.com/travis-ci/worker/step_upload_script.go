package worker

import (
	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/metrics"
	gocontext "golang.org/x/net/context"
)

type stepUploadScript struct{}

func (s *stepUploadScript) Run(state multistep.StateBag) multistep.StepAction {
	ctx := state.Get("ctx").(gocontext.Context)
	buildJob := state.Get("buildJob").(Job)

	instance := state.Get("instance").(backend.Instance)
	script := state.Get("script").([]byte)

	err := instance.UploadScript(ctx, script)
	if err != nil {
		errMetric := "worker.job.upload.error"
		if err == backend.ErrStaleVM {
			errMetric += ".stalevm"
		}
		metrics.Mark(errMetric)

		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't upload script")
		err := buildJob.Requeue()
		if err != nil {
			context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't requeue job")
		}

		return multistep.ActionHalt
	}

	context.LoggerFromContext(ctx).Info("uploaded script")

	return multistep.ActionContinue
}

func (s *stepUploadScript) Cleanup(state multistep.StateBag) {
	// Nothing to clean up
}
