// Package workerintegration contains various integration tests for Worker.
package workerintegration

import (
	"fmt"
	"os"
	"os/exec"
	"path"
)

func spawnWorkerProcess(env map[string]string) (*exec.Cmd, error) {
	workerPath := os.Getenv("WORKER_PATH")
	if workerPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}

		workerPath = path.Join(cwd, "..", "..", "..", "..", "..", "bin", "travis-worker")
	}
	cmd := exec.Command(workerPath)

	for key, value := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}

	return cmd, nil
}
