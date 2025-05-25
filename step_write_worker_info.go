package worker

import (
	gocontext "context"
	"fmt"
	"strings"

	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/backend"
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
				fmt.Sprintf("[OK \033[33;1mUsing custom build environment image %s %s. See <link to the documentation>.\033[0m", usedCustomImageName, instance.ImageName()),
			}, "\n")))
		}
	}

	return multistep.ActionContinue
}

func (s *stepWriteWorkerInfo) Cleanup(state multistep.StateBag) {}
