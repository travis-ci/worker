package worker

import (
	"fmt"
	"strings"

	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/backend"
)

type stepWriteWorkerInfo struct {
}

func (s *stepWriteWorkerInfo) Run(state multistep.StateBag) multistep.StepAction {
	logWriter := state.Get("logWriter").(LogWriter)
	instance := state.Get("instance").(backend.Instance)

	if hostname, ok := state.Get("hostname").(string); ok && hostname != "" {
		_, _ = writeFold(logWriter, "worker_info", []byte(strings.Join([]string{
			"\033[33;1mWorker information\033[0m",
			fmt.Sprintf("hostname: %s", hostname),
			fmt.Sprintf("version: %s %s", VersionString, RevisionURLString),
			fmt.Sprintf("instance: %s", instance.ID()),
			fmt.Sprintf("startup: %v", instance.StartupDuration()),
		}, "\n")))
	}

	return multistep.ActionContinue
}

func (s *stepWriteWorkerInfo) Cleanup(state multistep.StateBag) {
}
