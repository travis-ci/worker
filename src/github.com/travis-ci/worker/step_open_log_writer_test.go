package worker

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	gocontext "golang.org/x/net/context"

	"github.com/mitchellh/multistep"
	"github.com/stretchr/testify/assert"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/config"
)

type fakeWriteFolder struct {
	lastFold string
	buf      *bytes.Buffer
}

func (w *fakeWriteFolder) WriteFold(name string, b []byte) (int, error) {
	w.lastFold = name

	folded := []byte(fmt.Sprintf("travis_fold start %s\n", name))
	folded = append(folded, b...)

	if string(folded[len(folded)-1]) != "\n" {
		folded = append(folded, []byte("\n")...)
	}

	folded = append(folded, []byte(fmt.Sprintf("travis_fold end %s\n", name))...)
	return w.buf.Write(folded)
}

func setupStepOpenLogWriter() (*stepOpenLogWriter, multistep.StateBag) {
	s := &stepOpenLogWriter{logTimeout: time.Second, maxLogLength: 4}

	bp, _ := backend.NewBackendProvider("fake", config.ProviderConfigFromMap(map[string]string{}))
	ctx := gocontext.TODO()
	instance, _ := bp.Start(ctx, nil)

	job := &fakeJob{
		payload: &JobPayload{
			Type: "job:test",
			Job: JobJobPayload{
				ID:     2,
				Number: "3.1",
			},
			Build: BuildPayload{
				ID:     1,
				Number: "3",
			},
			Repository: RepositoryPayload{
				ID:   4,
				Slug: "green-eggs/ham",
			},
			UUID:     "foo-bar",
			Config:   map[string]interface{}{},
			Timeouts: TimeoutsPayload{},
		},
	}

	state := &multistep.BasicStateBag{}
	state.Put("ctx", ctx)
	state.Put("buildJob", job)
	state.Put("instance", instance)

	return s, state
}

func TestStepOpenLogWriter_Run(t *testing.T) {
	s, state := setupStepOpenLogWriter()
	action := s.Run(state)
	assert.Equal(t, multistep.ActionContinue, action)
}

func TestStepOpenLogWriter_writeUsingWorker(t *testing.T) {
	s, state := setupStepOpenLogWriter()

	w := &fakeWriteFolder{buf: bytes.NewBufferString("")}
	s.writeUsingWorker(state, w)
	assert.Equal(t, "", w.lastFold)
	assert.Equal(t, "", w.buf.String())

	state.Put("hostname", "frizzlefry.example.local")

	w = &fakeWriteFolder{buf: bytes.NewBufferString("")}
	s.writeUsingWorker(state, w)
	out := w.buf.String()

	assert.Equal(t, "worker_summary", w.lastFold)
	assert.Contains(t, out, "travis_fold start worker_summary\n")
	assert.Contains(t, out, "\nUsing worker:\n")
	assert.Contains(t, out, "\nhostname=frizzlefry.example.local\n")
	assert.Contains(t, out, "\nid=fake\n")
	assert.Contains(t, out, "\nversion="+VersionString+"\n")
	assert.Contains(t, out, "\ntravis_fold end worker_summary\n")
}
