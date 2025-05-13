package worker

import (
	"fmt"
	"time"

	gocontext "context"

	"github.com/mitchellh/multistep"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
	workererrors "github.com/travis-ci/worker/errors"
	"github.com/travis-ci/worker/image"
	"go.opencensus.io/trace"
)

type stepStartInstance struct {
	provider        backend.Provider
	startTimeout    time.Duration
	artifactManager *image.ArtifactManager
	ownerId         int
	userId          int
}

func (s *stepStartInstance) Run(state multistep.StateBag) multistep.StepAction {
	buildJob := state.Get("buildJob").(Job)
	ctx := state.Get("ctx").(gocontext.Context)
	logWriter := state.Get("logWriter").(LogWriter)

	logger := context.LoggerFromContext(ctx).WithField("self", "step_start_instance")
	logger.Info("DEBUGDEBUG stepStartInstance.Run")
	defer context.TimeSince(ctx, "step_start_instance_run", time.Now())

	ctx, span := trace.StartSpan(ctx, "StartInstance.Run")
	defer span.End()

	logger.Info("starting instance")

	preTimeoutCtx := ctx

	ctx, cancel := gocontext.WithTimeout(ctx, s.startTimeout)
	defer cancel()

	startTime := time.Now()

	var (
		instance backend.Instance
		err      error
	)

	userId := state.Get("usedCustomImageId").(int)
	ownerId := state.Get("ownerId").(int)
	ownerType := state.Get("ownerType").(string)
	createdCustomImageId := state.Get("createdCustomImageId").(int)
	usedCustomImageId := state.Get("usedCustomImageId").(int)

	buildJob.StartAttributes().ArtifactManager = s.artifactManager
	buildJob.StartAttributes().UserId = userId
	buildJob.StartAttributes().OwnerId = ownerId
	buildJob.StartAttributes().OwnerType = ownerType
	buildJob.StartAttributes().CreatedCustomImageId = createdCustomImageId
	buildJob.StartAttributes().UsedCustomImageId = usedCustomImageId

	if s.provider.SupportsProgress() && buildJob.StartAttributes().ProgressType != "" {
		var progresser backend.Progresser
		switch buildJob.StartAttributes().ProgressType {
		case "text":
			progresser = backend.NewTextProgresser(logWriter)
			_, _ = writeFoldStart(logWriter, "step_start_instance", []byte("\033[33;1mStarting instance\033[0m\r\n"))
			defer func() {
				_, err := writeFoldEnd(logWriter, "step_start_instance", []byte(""))
				if err != nil {
					logger.WithFields(logrus.Fields{
						"err": err,
					}).Error("couldn't write fold end")
				}
			}()
		default:
			logger.WithField("progress_type", buildJob.StartAttributes().ProgressType).Warn("unknown progress type")
			progresser = &backend.NullProgresser{}
		}

		instance, err = s.provider.StartWithProgress(ctx, buildJob.StartAttributes(), progresser)
	} else {
		instance, err = s.provider.Start(ctx, buildJob.StartAttributes())
	}

	if err != nil {
		state.Put("err", err)

		span.SetStatus(trace.Status{
			Code:    trace.StatusCodeUnavailable,
			Message: err.Error(),
		})

		jobAbortErr, ok := errors.Cause(err).(workererrors.JobAbortError)
		if ok {
			logWriter.WriteAndClose([]byte(jobAbortErr.UserFacingErrorMessage()))

			err = buildJob.Finish(preTimeoutCtx, FinishStateErrored)
			if err != nil {
				logger.WithField("err", err).WithField("state", FinishStateErrored).Error("couldn't mark job as finished")
			}

			return multistep.ActionHalt
		}

		logger.WithFields(logrus.Fields{
			"err":           err,
			"start_timeout": s.startTimeout,
		}).Error("couldn't start instance, attempting requeue")
		context.CaptureError(ctx, err)

		err := buildJob.Requeue(preTimeoutCtx)
		if err != nil {
			logger.WithField("err", err).Error("couldn't requeue job")
		}

		return multistep.ActionHalt
	}

	logger.WithFields(logrus.Fields{
		"boot_duration_ms": time.Since(startTime).Seconds() * 1e3,
		"instance_id":      instance.ID(),
		"image_name":       instance.ImageName(),
		"version":          VersionString,
		"warmed":           instance.Warmed(),
	}).Info("started instance")

	state.Put("instance", instance)

	return multistep.ActionContinue
}

func (s *stepStartInstance) Cleanup(state multistep.StateBag) {
	ctx := state.Get("ctx").(gocontext.Context)

	defer context.TimeSince(ctx, "step_start_instance_cleanup", time.Now())

	ctx, span := trace.StartSpan(ctx, "StartInstance.Cleanup")
	defer span.End()

	instance, ok := state.Get("instance").(backend.Instance)
	logger := context.LoggerFromContext(ctx).WithField("self", "step_start_instance")
	if !ok {
		logger.Info("no instance to stop")
		return
	}
	logger.Info("DEBUGDEBUG stepStartInstance.Cleanup")
	skipShutdown, ok := state.Get("skipShutdown").(bool)
	if ok && skipShutdown {
		logger.WithField("instance", instance).Error("skipping shutdown, VM will be left running")
		return
	}

	createdCustomImageId, ok1 := state.Get("createdCustomImageId").(int)
	ownerId, ok2 := state.Get("ownerId").(int)
	ownerType, ok3 := state.Get("ownerType").(string)
	if ok1 && ok2 && ok3 && createdCustomImageId != 0 {
		createCustomImageName := s.artifactManager.GenerateCustomImageName(ownerId, ownerType, createdCustomImageId)
		logger.WithField("instance", instance).Info(fmt.Sprintf("creating custom image id: %d", createdCustomImageId))
		if err := instance.StopOnly(ctx); err != nil {
			logger.WithFields(logrus.Fields{"err": err, "instance": instance}).Warn("couldn't stop instance")
		} else {
			logger.Info("stopped instance")
		}
		if size, err := instance.CreateImage(ctx, createCustomImageName); err != nil {
			logger.WithFields(logrus.Fields{"err": err, "instance": instance}).Warn("couldn't create custom image")
		} else {
			logger.Info(fmt.Sprintf("custom image created, size: %d", size))

			s.artifactManager.UpdateImageSize(ctx, createdCustomImageId, size)
		}
	}

	if err := instance.Stop(ctx); err != nil {
		logger.WithFields(logrus.Fields{"err": err, "instance": instance}).Warn("couldn't stop instance")
	} else {
		logger.Info("stopped and deleted instance")
	}
}
