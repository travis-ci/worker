package worker

import (
	"context"
	"time"

	"github.com/Sirupsen/logrus"
	simplejson "github.com/bitly/go-simplejson"
	"github.com/travis-ci/worker/backend"
)

func init() {
	logrus.SetLevel(logrus.FatalLevel)
}

type fakeJobQueue struct {
	c chan Job
}

func (jq *fakeJobQueue) Jobs(ctx context.Context) (<-chan Job, error) {
	return jq.c, nil
}

func (jq *fakeJobQueue) Name() string { return "fake" }

func (jq *fakeJobQueue) Cleanup() error { return nil }

type fakeJob struct {
	payload         *JobPayload
	rawPayload      *simplejson.Json
	startAttributes *backend.StartAttributes

	events []string
}

func (fj *fakeJob) Payload() *JobPayload {
	return fj.payload
}

func (fj *fakeJob) RawPayload() *simplejson.Json {
	return fj.rawPayload
}

func (fj *fakeJob) StartAttributes() *backend.StartAttributes {
	return fj.startAttributes
}

func (fj *fakeJob) Received() error {
	fj.events = append(fj.events, "received")
	return nil
}

func (fj *fakeJob) Started() error {
	fj.events = append(fj.events, "started")
	return nil
}

func (fj *fakeJob) Error(ctx context.Context, msg string) error {
	fj.events = append(fj.events, "errored")
	return nil
}

func (fj *fakeJob) Requeue(ctx context.Context) error {
	fj.events = append(fj.events, "requeued")
	return nil
}

func (fj *fakeJob) Finish(ctx context.Context, state FinishState) error {
	fj.events = append(fj.events, string(state))
	return nil
}

func (fj *fakeJob) LogWriter(ctx context.Context, defaultLogTimeout time.Duration) (LogWriter, error) {
	return &fakeLogWriter{}, nil
}

func (fj *fakeJob) Name() string { return "fake" }

type fakeLogWriter struct{}

func (flw *fakeLogWriter) Write(p []byte) (int, error) {
	return 0, nil
}

func (flw *fakeLogWriter) Close() error {
	return nil
}

func (flw *fakeLogWriter) WriteAndClose(p []byte) (int, error) {
	return 0, nil
}

func (flw *fakeLogWriter) Timeout() <-chan time.Time {
	return make(chan time.Time)
}

func (flw *fakeLogWriter) SetMaxLogLength(l int) {}
