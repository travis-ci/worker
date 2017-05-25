package worker

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/pborman/uuid"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/config"
	workerctx "github.com/travis-ci/worker/context"
)

type buildScriptGeneratorFunction func(context.Context, Job) ([]byte, error)

func (bsg buildScriptGeneratorFunction) Generate(ctx context.Context, job Job) ([]byte, error) {
	return bsg(ctx, job)
}

func TestProcessor(t *testing.T) {
	uuid := uuid.NewRandom()
	ctx := workerctx.FromProcessor(context.TODO(), uuid.String())

	provider, err := backend.NewBackendProvider("fake", config.ProviderConfigFromMap(map[string]string{
		"LOG_OUTPUT": "hello, world",
	}))
	if err != nil {
		t.Error(err)
	}

	generator := buildScriptGeneratorFunction(func(ctx context.Context, job Job) ([]byte, error) {
		return []byte("hello, world"), nil
	})

	jobChan := make(chan Job)
	jobQueue := &fakeJobQueue{c: jobChan}
	cancellationBroadcaster := NewCancellationBroadcaster()

	processor, err := NewProcessor(ctx, "test-hostname", jobQueue, provider, generator, cancellationBroadcaster, ProcessorConfig{
		HardTimeout:         2 * time.Second,
		LogTimeout:          time.Second,
		ScriptUploadTimeout: 3 * time.Second,
		StartupTimeout:      4 * time.Second,
		MaxLogLength:        4500000,
	})
	if err != nil {
		t.Error(err)
	}

	doneChan := make(chan struct{})
	go func() {
		processor.Run()
		doneChan <- struct{}{}
	}()

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
		startAttributes: &backend.StartAttributes{},
	}
	jobChan <- job

	processor.GracefulShutdown()
	<-doneChan

	if processor.ProcessedCount != 1 {
		t.Errorf("processor.ProcessedCount = %d, expected %d", processor.ProcessedCount, 1)
	}

	expectedEvents := []string{"received", "started", string(FinishStatePassed)}
	if !reflect.DeepEqual(expectedEvents, job.events) {
		t.Errorf("job.events = %#v, expected %#v", job.events, expectedEvents)
	}
}
