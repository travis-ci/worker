package lib

import (
	"io"
	"time"

	"github.com/bitly/go-simplejson"
	"github.com/travis-ci/worker/lib/backend"
	"golang.org/x/net/context"
)

// JobPayload is the payload we receive over RabbitMQ.
type JobPayload struct {
	Type       string                 `json:"type"`
	Job        JobJobPayload          `json:"job"`
	Build      BuildPayload           `json:"source"`
	Repository RepositoryPayload      `json:"repository"`
	UUID       string                 `json:"uuid"`
	Config     map[string]interface{} `json:"config"`
}

type JobJobPayload struct {
	ID     uint64 `json:"id"`
	Number string `json:"number"`
}

type BuildPayload struct {
	ID     uint64 `json:"id"`
	Number string `json:"number"`
}

type RepositoryPayload struct {
	ID   uint64 `json:"id"`
	Slug string `json:"slug"`
}

// FinishState is the state that a job finished with (such as pass/fail/etc.).
// You should not provide a string directly, but use one of the FinishStateX
// constants defined in this package.
type FinishState string

// Valid finish states for the FinishState type
const (
	FinishStatePassed    FinishState = "passed"
	FinishStateFailed    FinishState = "failed"
	FinishStateErrored   FinishState = "errored"
	FinishStateCancelled FinishState = "cancelled"
)

// A Job ties togeher all the elements required for a build job
type Job interface {
	Payload() JobPayload
	RawPayload() *simplejson.Json
	StartAttributes() backend.StartAttributes

	Received() error
	Started() error
	Error(context.Context, string) error
	Requeue() error
	Finish(FinishState) error

	LogWriter(context.Context) (LogWriter, error)
}

type LogWriter interface {
	io.WriteCloser
	WriteAndClose([]byte) (int, error)
	SetTimeout(time.Duration)
	Timeout() <-chan time.Time
	SetMaxLogLength(int)
}
