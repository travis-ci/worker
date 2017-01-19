package worker

import (
	"bytes"
	"testing"

	gocontext "golang.org/x/net/context"

	"github.com/mitchellh/multistep"
	"github.com/stretchr/testify/assert"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/config"
)

func setupStepWriteWorkerInfo() (*stepWriteWorkerInfo, *bytes.Buffer, multistep.StateBag) {
	s := &stepWriteWorkerInfo{}

	bp, _ := backend.NewBackendProvider("fake",
		config.ProviderConfigFromMap(map[string]string{
			"STARTUP_DURATION": "42.17s",
		}))

	ctx := gocontext.TODO()
	instance, _ := bp.Start(ctx, nil)

	logWriter := bytes.NewBufferString("")

	state := &multistep.BasicStateBag{}
	state.Put("logWriter", logWriter)
	state.Put("instance", instance)
	state.Put("hostname", "frizzlefry.example.local")

	return s, logWriter, state
}

func TestStepWriteWorkerInfo_Run(t *testing.T) {
	s, out, state := setupStepWriteWorkerInfo()

	s.Run(state)

	assert.Contains(t, out, "travis_fold:start:worker_info\r\033[0K")
	assert.Contains(t, out, "\033[33;1mWorker information\033[0m\n")
	assert.Contains(t, out, "\nhostname: frizzlefry.example.local\n")
	assert.Contains(t, out, "\nversion: "+VersionString+" "+RevisionURLString+"\n")
	assert.Contains(t, out, "\ninstance: fake\n")
	assert.Contains(t, out, "\nstartup: 42.17s\n")
	assert.Contains(t, out, "\ntravis_fold:end:worker_info\r\033[0K")
}
