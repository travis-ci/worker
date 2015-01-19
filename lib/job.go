package lib

import "io"

// JobPayload is the payload we receive over RabbitMQ.
type JobPayload struct {
	Type       string            `json:"type"`
	Job        JobJobPayload     `json:"job"`
	Build      BuildPayload      `json:"source"`
	Repository RepositoryPayload `json:"repository"`
	UUID       string            `json:"uuid"`
}

type JobJobPayload struct {
	ID uint64 `json:"id"`
}

type BuildPayload struct {
	ID uint64 `json:"id"`
}

type RepositoryPayload struct {
	ID uint64 `json:"id"`
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

	Error(error) error
	Requeue() error
	Finish(FinishState) error

	LogWriter() (io.WriteCloser, error)
}
