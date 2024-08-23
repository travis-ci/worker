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

func (w *byteBufferLogWriter) SetJobStarted(meta *JobStartedMeta) {
}

func (w *byteBufferLogWriter) SetCancelFunc(_ gocontext.CancelFunc) {
}

func (w *byteBufferLogWriter) MaxLengthReached() bool {
	return false
}

func setupStepWriteWorkerInfo(premium bool, gpu bool) (*stepWriteWorkerInfo, *byteBufferLogWriter, multistep.StateBag) {
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
	state.Put("ctx", ctx)
	state.Put("logWriter", logWriter)
	state.Put("instance", instance)
	state.Put("hostname", "frizzlefry.example.local")
	if (premium) {
		if gpu {
			state.Put("buildJob", &fakeJob{payload: &JobPayload{Job: JobJobPayload{ID: 4}, VMType: "premium", VMSize: "2x-large", VMConfig: backend.VmConfig{GpuCount: 1, GpuType: "nvidia-tesla-p100"}}})
		} else {
			state.Put("buildJob", &fakeJob{payload: &JobPayload{Job: JobJobPayload{ID: 4}, VMType: "premium", VMSize: "2x-large"}})
		}
	} else {
		state.Put("buildJob", &fakeJob{payload: &JobPayload{Job: JobJobPayload{ID: 4}}})
	}
	return s, logWriter, state
}

func TestStepWriteWorkerInfoPremiumNoGPU_Run(t *testing.T) {
	s, logWriter, state := setupStepWriteWorkerInfo(true, false)

	s.Run(state)

	out := logWriter.String()
	assert.Contains(t, out, "\nvm: premium, size: 2x-large")
}

func TestStepWriteWorkerInfoPremiumGPU_Run(t *testing.T) {
	s, logWriter, state := setupStepWriteWorkerInfo(true, true)

	s.Run(state)

	out := logWriter.String()
	assert.Contains(t, out, "\nvm: premium, size: 2x-large, gpu count: 1, gpu type: nvidia-tesla-p100")
}

func TestStepWriteWorkerInfo_Run(t *testing.T) {
	s, logWriter, state := setupStepWriteWorkerInfo(false, false)

	s.Run(state)

	out := logWriter.String()
	assert.Contains(t, out, "travis_fold:start:worker_info\r\033[0K")
	assert.Contains(t, out, "\033[33;1mWorker information\033[0m\n")
	assert.Contains(t, out, "\nhostname: frizzlefry.example.local\n")
	assert.Contains(t, out, "\nversion: "+VersionString+" "+RevisionURLString+"\n")
	assert.Contains(t, out, "\ninstance: fake fake (via fake)\n")
	assert.Contains(t, out, "\nstartup: 42.17s\n")
	assert.Contains(t, out, "\nvm: default")
	assert.Contains(t, out, "\ntravis_fold:end:worker_info\r\033[0K")
}
