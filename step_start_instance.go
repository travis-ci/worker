package worker

import (
	"time"

	gocontext "context"

	"github.com/Sirupsen/logrus"
	"github.com/mitchellh/multistep"
	"github.com/pkg/errors"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
	workererrors "github.com/travis-ci/worker/errors"
)

type stepStartInstance struct {
	provider     backend.Provider
	startTimeout time.Duration
}

func (s *stepStartInstance) Run(state multistep.StateBag) multistep.StepAction {
	buildJob := state.Get("buildJob").(Job)
	ctx := state.Get("ctx").(gocontext.Context)

	context.LoggerFromContext(ctx).Info("starting instance")

	ctx, cancel := gocontext.WithTimeout(ctx, s.startTimeout)
	defer cancel()

	startTime := time.Now()

	instance, err := s.provider.Start(ctx, buildJob.StartAttributes())
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't start instance")
		context.CaptureError(ctx, err)

		jobAbortErr, ok := errors.Cause(err).(workererrors.JobAbortError)
		if ok {
			logWriter := state.Get("logWriter").(LogWriter)
			logWriter.WriteAndClose([]byte(jobAbortErr.UserFacingErrorMessage()))

			err = buildJob.Finish(ctx, FinishStateErrored)
			if err != nil {
				context.LoggerFromContext(ctx).WithField("err", err).WithField("state", FinishStateErrored).Error("couldn't mark job as finished")
			}

			return multistep.ActionHalt
		}

		err := buildJob.Requeue(ctx)
		if err != nil {
			context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't requeue job")
		}

		return multistep.ActionHalt
	}

	context.LoggerFromContext(ctx).WithField("boot_time", time.Since(startTime)).Info("started instance")

	state.Put("instance", instance)

	return multistep.ActionContinue
}

func (s *stepStartInstance) Cleanup(state multistep.StateBag) {
	ctx := state.Get("ctx").(gocontext.Context)
	instance, ok := state.Get("instance").(backend.Instance)
	if !ok {
		context.LoggerFromContext(ctx).Info("no instance to stop")
		return
	}

	skipShutdown, ok := state.Get("skipShutdown").(bool)
	if ok && skipShutdown {
		context.LoggerFromContext(ctx).WithFields(logrus.Fields{"instance": instance}).Error("skipping shutdown, VM will be left running")
		return
	}

	if err := instance.Stop(ctx); err != nil {
		context.LoggerFromContext(ctx).WithFields(logrus.Fields{"err": err, "instance": instance}).Warn("couldn't stop instance")
	} else {
		context.LoggerFromContext(ctx).Info("stopped instance")
	}
}
