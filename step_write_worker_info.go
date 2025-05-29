package worker

import (
	gocontext "context"
	"fmt"
	"strings"

	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
	"go.opencensus.io/trace"
)

type stepWriteWorkerInfo struct {
}

func (s *stepWriteWorkerInfo) Run(state multistep.StateBag) multistep.StepAction {
	logWriter := state.Get("logWriter").(LogWriter)
	buildJob := state.Get("buildJob").(Job)
	usedCustomImageId := state.Get("usedCustomImageId").(int)
	usedCustomImageName := state.Get("usedCustomImageName").(string)
	instance := state.Get("instance").(backend.Instance)
	ctx := state.Get("ctx").(gocontext.Context)

	_, span := trace.StartSpan(ctx, "WriteWorkerInfo.Run")
	defer span.End()

	if hostname, ok := state.Get("hostname").(string); ok && hostname != "" {
		_, _ = writeFold(logWriter, "worker_info", []byte(strings.Join([]string{
			"\033[33;1mWorker information\033[0m",
			fmt.Sprintf("hostname: %s", hostname),
			fmt.Sprintf("version: %s %s", VersionString, RevisionURLString),
			fmt.Sprintf("instance: %s %s (via %s)", instance.ID(), instance.ImageName(), buildJob.Name()),
			fmt.Sprintf("startup: %v", instance.StartupDuration()),
		}, "\n")))
		if usedCustomImageId != 0 {
			writeSingleLine(logWriter, []byte(strings.Join([]string{
				fmt.Sprintf("Using custom build environment image %s %s. See <link to the documentation>.", usedCustomImageName, instance.ImageName()),
			}, "\n")))
		}
		if usedCustomImageId == 0 && usedCustomImageName != "" {
			msg := fmt.Sprintf("Cannot find custom build environment identifier %s under the account managing this repository in Travis.\n", usedCustomImageName)
			logWriter.WriteAndClose([]byte(msg))
			err := buildJob.Finish(ctx, FinishStateErrored)
			if err != nil {
				logger := context.LoggerFromContext(ctx).WithField("self", "step_write_worker_info")
				logger.WithField("err", err).Error("couldn't error the job")
			}
			return multistep.ActionHalt
		}
	}

	return multistep.ActionContinue
}

func (s *stepWriteWorkerInfo) Cleanup(state multistep.StateBag) {}
