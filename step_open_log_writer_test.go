package worker

import (
	"bytes"
	"testing"
	"time"

	gocontext "golang.org/x/net/context"

	"github.com/mitchellh/multistep"
	"github.com/stretchr/testify/assert"
	"github.com/travis-ci/worker/backend"
)

func setupStepOpenLogWriter() (*stepOpenLogWriter, multistep.StateBag) {
	s := &stepOpenLogWriter{logTimeout: time.Second, maxLogLength: 4}

	bp, _ := backend.NewBackendProvider("fake", &testConfigGetter{
		m: map[string]interface{}{
			"startup-duration": 42170 * time.Millisecond,
		},
	})

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

	w := bytes.NewBufferString("")
	s.writeUsingWorker(state, w)
	assert.Equal(t, "", w.String())

	state.Put("hostname", "frizzlefry.example.local")

	w = bytes.NewBufferString("")
	s.writeUsingWorker(state, w)
	out := w.String()

	assert.Contains(t, out, "travis_fold:start:worker_info\r\033[0K")
	assert.Contains(t, out, "\033[33;1mWorker information\033[0m\n")
	assert.Contains(t, out, "\nhostname: frizzlefry.example.local\n")
	assert.Contains(t, out, "\nversion: "+VersionString+" "+RevisionURLString+"\n")
	assert.Contains(t, out, "\ninstance: fake\n")
	assert.Contains(t, out, "\nstartup: 42.17s\n")
	assert.Contains(t, out, "\ntravis_fold:end:worker_info\r\033[0K")
}
