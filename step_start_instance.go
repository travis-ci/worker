package worker

import (
	"time"

	gocontext "context"

	"github.com/mitchellh/multistep"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
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
	procCtx := state.Get("procCtx").(gocontext.Context)
	ctx := state.Get("ctx").(gocontext.Context)
	logger := context.LoggerFromContext(ctx).WithField("self", "step_start_instance")

	logger.Info("starting instance")

	ctx, cancel := gocontext.WithTimeout(ctx, s.startTimeout)
	defer cancel()

	startTime := time.Now()

	instance, err := s.provider.Start(ctx, buildJob.StartAttributes())
	if err != nil {
		logger.WithField("err", err).Error("couldn't start instance")
		context.CaptureError(ctx, err)

		jobAbortErr, ok := errors.Cause(err).(workererrors.JobAbortError)
		if ok {
			logWriter := state.Get("logWriter").(LogWriter)
			logWriter.WriteAndClose([]byte(jobAbortErr.UserFacingErrorMessage()))

			err = buildJob.Finish(procCtx, FinishStateErrored)
			if err != nil {
				logger.WithField("err", err).WithField("state", FinishStateErrored).Error("couldn't mark job as finished")
			}

			return multistep.ActionHalt
		}

		err := buildJob.Requeue(procCtx)
		if err != nil {
			logger.WithField("err", err).Error("couldn't requeue job")
		}

		return multistep.ActionHalt
	}

	logger.WithField("boot_time", time.Since(startTime)).WithField("instance_id", instance.ID()).Info("started instance")

	ctx = context.FromInstanceID(ctx, instance.ID())
	state.Put("instance", instance)
	state.Put("ctx", ctx)

	return multistep.ActionContinue
}

func (s *stepStartInstance) Cleanup(state multistep.StateBag) {
	ctx := state.Get("ctx").(gocontext.Context)
	instance, ok := state.Get("instance").(backend.Instance)
	logger := context.LoggerFromContext(ctx).WithField("self", "step_start_instance")
	if !ok {
		logger.Info("no instance to stop")
		return
	}

	skipShutdown, ok := state.Get("skipShutdown").(bool)
	if ok && skipShutdown {
		logger.WithField("instance", instance).Error("skipping shutdown, VM will be left running")
		return
	}

	if err := instance.Stop(ctx); err != nil {
		logger.WithFields(logrus.Fields{"err": err, "instance": instance}).Warn("couldn't stop instance")
	} else {
		logger.Info("stopped instance")
	}
}
