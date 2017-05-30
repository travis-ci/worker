package worker

import (
	"bytes"
	"testing"
	"time"

	gocontext "context"

	"github.com/mitchellh/multistep"
	"github.com/stretchr/testify/assert"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/config"
)

type byteBufferLogWriter struct {
	*bytes.Buffer
}

func (w *byteBufferLogWriter) Close() error {
	return nil
}

func (w *byteBufferLogWriter) WriteAndClose(p []byte) (int, error) {
	return w.Write(p)
}

func (w *byteBufferLogWriter) Timeout() <-chan time.Time {
	return make(<-chan time.Time)
}

func (w *byteBufferLogWriter) SetMaxLogLength(m int) {
}

func setupStepWriteWorkerInfo() (*stepWriteWorkerInfo, *byteBufferLogWriter, multistep.StateBag) {
	s := &stepWriteWorkerInfo{}

	bp, _ := backend.NewBackendProvider("fake",
		config.ProviderConfigFromMap(map[string]string{
			"STARTUP_DURATION": "42.17s",
		}))

	ctx := gocontext.TODO()
	instance, _ := bp.Start(ctx, nil)

	logWriter := &byteBufferLogWriter{
		bytes.NewBufferString(""),
	}

	state := &multistep.BasicStateBag{}
	state.Put("logWriter", logWriter)
	state.Put("instance", instance)
	state.Put("hostname", "frizzlefry.example.local")

	return s, logWriter, state
}

func TestStepWriteWorkerInfo_Run(t *testing.T) {
	s, logWriter, state := setupStepWriteWorkerInfo()

	s.Run(state)

	out := logWriter.String()
	assert.Contains(t, out, "travis_fold:start:worker_info\r\033[0K")
	assert.Contains(t, out, "\033[33;1mWorker information\033[0m\n")
	assert.Contains(t, out, "\nhostname: frizzlefry.example.local\n")
	assert.Contains(t, out, "\nversion: "+VersionString+" "+RevisionURLString+"\n")
	assert.Contains(t, out, "\ninstance: fake\n")
	assert.Contains(t, out, "\nstartup: 42.17s\n")
	assert.Contains(t, out, "\ntravis_fold:end:worker_info\r\033[0K")
}
