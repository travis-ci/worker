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

func(s *stepWriteWorkerInfo) getVMConfig(buildJob Job) string {
	if buildJob.Payload().VMType == "premium" {
		gpuInfo := ""
		if buildJob.Payload().VMConfig.GpuCount > 0 {
			gpuInfo = fmt.Sprintf(", gpu count: %d, gpu type: %s", buildJob.Payload().VMConfig.GpuCount, buildJob.Payload().VMConfig.GpuType)
		}
		return fmt.Sprintf("vm: premium, size: %s%s", buildJob.Payload().VMSize, gpuInfo)
	} else {
		return fmt.Sprintf("vm: default")
	}
}

func (s *stepWriteWorkerInfo) Run(state multistep.StateBag) multistep.StepAction {
	logWriter := state.Get("logWriter").(LogWriter)
	buildJob := state.Get("buildJob").(Job)
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
			s.getVMConfig(buildJob),
			fmt.Sprintf("startup: %v", instance.StartupDuration()),
		}, "\n")))
	}

	return multistep.ActionContinue
}

func (s *stepWriteWorkerInfo) Cleanup(state multistep.StateBag) {}
