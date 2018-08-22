package worker

import (
	"os"
	"time"

	gocontext "context"

	"github.com/mitchellh/multistep"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/metrics"
)

type stepDownloadTrace struct {
	persister BuildTracePersister
}

func (s *stepDownloadTrace) Run(state multistep.StateBag) multistep.StepAction {
	if s.persister == nil {
		return multistep.ActionContinue
	}

	ctx := state.Get("ctx").(gocontext.Context)
	buildJob := state.Get("buildJob").(Job)
	processedAt := state.Get("processedAt").(time.Time)

	instance := state.Get("instance").(backend.Instance)

	logger := context.LoggerFromContext(ctx).WithField("self", "step_download_trace")

	// ctx, cancel := gocontext.WithTimeout(ctx, s.uploadTimeout)
	// defer cancel()

	// downloading the trace is best-effort, so we continue in any case

	buf, err := instance.DownloadTrace(ctx)
	if err != nil {
		if err == backend.ErrDownloadTraceNotImplemented || os.IsNotExist(errors.Cause(err)) {
			logger.WithFields(logrus.Fields{
				"err": err,
			}).Info("skipping trace download")

			return multistep.ActionContinue
		}

		metrics.Mark("worker.job.trace.download.error")

		logger.WithFields(logrus.Fields{
			"err": err,
		}).Error("couldn't download trace")
		context.CaptureError(ctx, err)

		return multistep.ActionContinue
	}

	logger.WithFields(logrus.Fields{
		"since_processed_ms": time.Since(processedAt).Seconds() * 1e3,
	}).Info("downloaded trace")

	err = s.persister.Persist(ctx, buildJob, buf)

	if err != nil {
		metrics.Mark("worker.job.trace.persist.error")

		logger.WithFields(logrus.Fields{
			"err": err,
		}).Error("couldn't persist trace")
		context.CaptureError(ctx, err)

		return multistep.ActionContinue
	}

	logger.WithFields(logrus.Fields{
		"since_processed_ms": time.Since(processedAt).Seconds() * 1e3,
	}).Info("persisted trace")

	return multistep.ActionContinue
}

func (s *stepDownloadTrace) Cleanup(state multistep.StateBag) {
	// Nothing to clean up
}
