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
	createdCustomImageName := state.Get("createdCustomImageName").(string)
	usedCustomImageId := state.Get("usedCustomImageId").(int)
	usedCustomImageName := state.Get("usedCustomImageName").(string)

	logger.Info(fmt.Sprintf("DEBUGDEBUG stepStartInstance.Run userId: %d", userId))
	logger.Info(fmt.Sprintf("DEBUGDEBUG stepStartInstance.Run ownerId: %d", ownerId))
	logger.Info(fmt.Sprintf("DEBUGDEBUG stepStartInstance.Run ownerType: %s", ownerType))
	logger.Info(fmt.Sprintf("DEBUGDEBUG stepStartInstance.Run createdCustomImageId: %d", createdCustomImageId))
	logger.Info(fmt.Sprintf("DEBUGDEBUG stepStartInstance.Run createdCustomImageName: %s", createdCustomImageName))
	logger.Info(fmt.Sprintf("DEBUGDEBUG stepStartInstance.Run usedCustomImageId: %d", usedCustomImageId))
	logger.Info(fmt.Sprintf("DEBUGDEBUG stepStartInstance.Run usedCustomImageName: %s", usedCustomImageName))

	buildJob.StartAttributes().ArtifactManager = s.artifactManager
	buildJob.StartAttributes().UserId = userId
	buildJob.StartAttributes().OwnerId = ownerId
	buildJob.StartAttributes().OwnerType = ownerType
	buildJob.StartAttributes().CreatedCustomImageId = createdCustomImageId
	buildJob.StartAttributes().CreatedCustomImageName = createdCustomImageName
	buildJob.StartAttributes().UsedCustomImageId = usedCustomImageId
	buildJob.StartAttributes().UsedCustomImageName = usedCustomImageName

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
	createdCustomImageName, ok2 := state.Get("createdCustomImageName").(string)
	ownerId, ok3 := state.Get("ownerId").(int)
	ownerType, ok4 := state.Get("ownerType").(string)
	logger.Info(fmt.Sprintf("DEBUGDEBUG stepStartInstance.Cleanup createdCustomImageId:%d", createdCustomImageId))
	logger.Info(fmt.Sprintf("DEBUGDEBUG stepStartInstance.Cleanup ownerId:%d", ownerId))
	logger.Info(fmt.Sprintf("DEBUGDEBUG stepStartInstance.Cleanup ownerType:%s", ownerType))
	if ok1 && ok2 && ok3 && ok4 && createdCustomImageId != 0 && createdCustomImageName != "" {
		createCustomImageName := s.artifactManager.GenerateCustomImageName(ownerId, ownerType, createdCustomImageId)
		logger.Info(fmt.Sprintf("DEBUGDEBUG stepStartInstance.Cleanup createCustomImageName:%s", createCustomImageName))
		logger.WithField("instance", instance).Info(fmt.Sprintf("creating custom image id: %d", createdCustomImageId))
		if err := instance.StopOnly(ctx); err != nil {
			logger.WithFields(logrus.Fields{"err": err, "instance": instance}).Warn("couldn't stop instance")
		} else {
			logger.Info("stopped instance")
		}
		if size, arch, os, err := instance.CreateImage(ctx, createCustomImageName); err != nil {
			logger.Info(fmt.Sprintf("DEBUGDEBUG stepStartInstance.Cleanup err: %v", err))
			logger.WithFields(logrus.Fields{"err": err, "instance": instance}).Warn("couldn't create custom image")
		} else {
			logger.Info(fmt.Sprintf("custom image created, size: %d", size))
			logger.Info(fmt.Sprintf("DEBUGDEBUG stepStartInstance.Cleanup size plus: %d %s %s %v", size, arch, os, err))
			_, err := s.artifactManager.UpdateImage(ctx, createdCustomImageId, size, arch, os)
			if err != nil {
				logger.WithFields(logrus.Fields{"err": err, "instance": instance}).Warn("couldn't create update image size")
			}
		}
	}

	if err := instance.Stop(ctx); err != nil {
		logger.WithFields(logrus.Fields{"err": err, "instance": instance}).Warn("couldn't stop instance")
	} else {
		logger.Info("stopped and deleted instance")
	}
}
