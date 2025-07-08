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
}

func (s *stepStartInstance) Run(state multistep.StateBag) multistep.StepAction {
	buildJob := state.Get("buildJob").(Job)
	ctx := state.Get("ctx").(gocontext.Context)
	logWriter := state.Get("logWriter").(LogWriter)

	logger := context.LoggerFromContext(ctx).WithField("self", "step_start_instance")
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

	userId := state.Get("userId").(int)
	ownerId := state.Get("ownerId").(int)
	ownerType := state.Get("ownerType").(string)
	createdCustomImageId := state.Get("createdCustomImageId").(int)
	createdCustomImageName := state.Get("createdCustomImageName").(string)
	usedCustomImageId := state.Get("usedCustomImageId").(int)
	usedCustomImageName := state.Get("usedCustomImageName").(string)

	buildJob.StartAttributes().ArtifactManager = s.artifactManager
	buildJob.StartAttributes().UserId = userId
	buildJob.StartAttributes().OwnerId = ownerId
	buildJob.StartAttributes().OwnerType = ownerType
	buildJob.StartAttributes().CreatedCustomImageId = createdCustomImageId
	buildJob.StartAttributes().CreatedCustomImageName = createdCustomImageName
	buildJob.StartAttributes().UsedCustomImageId = usedCustomImageId
	buildJob.StartAttributes().UsedCustomImageName = usedCustomImageName

	if createdCustomImageId != 0 {
		_, err = s.artifactManager.UpdateCreatingImage(ctx, createdCustomImageId, userId)
		if err != nil {
			logger.WithFields(logrus.Fields{"err": err, "instance": instance}).Warn("couldn't update image creating status")
		}
	}

	if usedCustomImageId != 0 {
		image, err := s.artifactManager.GetImage(ctx, usedCustomImageName, userId, ownerId, ownerType)
		if err != nil {
			logger.WithField("err", err).Error("failed to call GetImage at ArtifactManager")
			msg := fmt.Sprintf("Cannot find custom build environment identifier %s under the account managing this repository in Travis.\n", usedCustomImageName)
			logWriter.WriteAndClose([]byte(msg))
			err := buildJob.Finish(ctx, FinishStateErrored)
			if err != nil {
				logger.WithField("err", err).Error("couldn't error the job")
			}
			return multistep.ActionHalt
		}

		if image.State == "creating" {
			err := buildJob.Requeue(preTimeoutCtx)
			if err != nil {
				logger.WithField("err", err).Error("couldn't requeue the job")
			}
			return multistep.ActionHalt
		}

		_, err = s.artifactManager.UseImage(ctx, usedCustomImageId, userId)
		if err != nil {
			logger.Error(fmt.Sprintf("failed to call UseImage at ArtifactManager %v", err))
			msg := fmt.Sprintf("Cannot find custom build environment identifier %s under the account managing this repository in Travis.\n", usedCustomImageName)
			logWriter.WriteAndClose([]byte(msg))
			err := buildJob.Finish(ctx, FinishStateErrored)
			if err != nil {
				logger.WithField("err", err).Error("couldn't error the job")
			}
			return multistep.ActionHalt
		}
	}
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
	logWriter := state.Get("logWriter").(LogWriter)
	buildJob := state.Get("buildJob").(Job)

	defer context.TimeSince(ctx, "step_start_instance_cleanup", time.Now())

	ctx, span := trace.StartSpan(ctx, "StartInstance.Cleanup")
	defer span.End()

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

	can_create := buildJob.FinishState() == FinishStatePassed || buildJob.Payload().AllowFailure
	createdCustomImageId, ok1 := state.Get("createdCustomImageId").(int)
	createdCustomImageName, ok2 := state.Get("createdCustomImageName").(string)
	ownerId, ok3 := state.Get("ownerId").(int)
	ownerType, ok4 := state.Get("ownerType").(string)
	userId, ok5 := state.Get("userId").(int)
	createConditionsOk := ok1 && ok2 && ok3 && ok4 && ok5 && createdCustomImageId != 0 && createdCustomImageName != ""

	if can_create && createConditionsOk {
		createCustomImageName := s.artifactManager.GenerateCustomImageName(ownerId, ownerType, createdCustomImageId)
		logger.WithField("instance", instance).Info(fmt.Sprintf("creating custom image id: %d, name: %s", createdCustomImageId, createCustomImageName))
		if err := instance.StopOnly(ctx); err != nil {
			logger.WithFields(logrus.Fields{"err": err, "instance": instance}).Warn("couldn't stop instance")
		} else {
			logger.Info("only stopped instance")
		}
		logWriterFunc := func(value string) {
			fmt.Fprintf(logWriter, value)
		}
		if size, arch, os, err := instance.CreateImage(ctx, createCustomImageName, logWriterFunc); err != nil {
			logger.WithFields(logrus.Fields{"err": err, "instance": instance}).Warn("couldn't create custom image")
			_, err := s.artifactManager.UpdateFailedImage(ctx, createdCustomImageId, "create error", userId)
			if err != nil {
				logger.WithFields(logrus.Fields{"err": err, "instance": instance}).Warn("couldn't update image fail status")
			}
			fmt.Fprintf(logWriter, "\nCouldn't create custom image.\n")
		} else {
			dist := buildJob.StartAttributes().Dist
			logger.Info(fmt.Sprintf("custom image id: %d, name: %s, arch: %s, os: %s, dist: %s created with size: %d", createdCustomImageId, createCustomImageName, arch, os, dist, size))
			_, err := s.artifactManager.UpdateImage(ctx, createdCustomImageId, size, arch, os, dist, userId)
			if err != nil {
				logger.WithFields(logrus.Fields{"err": err, "instance": instance}).Warn("couldn't update image size")
			} else {
				fmt.Fprintf(logWriter, "\nCustom image successfully created.\n")
			}
		}
		if err := instance.Stop(ctx); err != nil { //Stop deletes instance
			logger.WithFields(logrus.Fields{"err": err, "instance": instance}).Warn("couldn't stop instance")
		} else {
			logger.Info("stopped instance")
		}
	} else if createConditionsOk {
		reason := "job failed"
		if buildJob.FinishState() == FinishStateErrored {
			reason = "job error"
		}
		_, err := s.artifactManager.UpdateFailedImage(ctx, createdCustomImageId, reason, userId)
		if err != nil {
			logger.WithFields(logrus.Fields{"err": err, "instance": instance}).Warn("couldn't update image status on build fail")
		}
	}

	if err := instance.Stop(ctx); err != nil {
		logger.WithFields(logrus.Fields{"err": err, "instance": instance}).Warn("couldn't stop instance")
	} else {
		logger.Info("stopped and deleted instance")
	}
}
