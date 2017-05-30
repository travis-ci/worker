package worker

import (
	"time"

	gocontext "context"

	"github.com/mitchellh/multistep"
	"github.com/pkg/errors"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/metrics"
)

type stepUploadScript struct {
	uploadTimeout time.Duration
}

func (s *stepUploadScript) Run(state multistep.StateBag) multistep.StepAction {
	ctx := state.Get("ctx").(gocontext.Context)
	buildJob := state.Get("buildJob").(Job)

	instance := state.Get("instance").(backend.Instance)
	script := state.Get("script").([]byte)

	ctx, cancel := gocontext.WithTimeout(ctx, s.uploadTimeout)
	defer cancel()

	err := instance.UploadScript(ctx, script)
	if err != nil {
		errMetric := "worker.job.upload.error"
		if errors.Cause(err) == backend.ErrStaleVM {
			errMetric += ".stalevm"
		}
		metrics.Mark(errMetric)

		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't upload script, attemping requeue")
		context.CaptureError(ctx, err)

		err := buildJob.Requeue(ctx)
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
