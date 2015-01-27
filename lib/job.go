package lib

import (
	"io"

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
	ID               uint64 `json:"id"`
	Number           string `json:"number"`
	Commit           string `json:"commit"`
	CommitRange      string `json:"commit_range"`
	CommitMessage    string `json:"commit_message"`
	Branch           string `json:"branch"`
	State            string `json:"state"`
	SecureEnvEnabled bool   `json:"secure_env_enabled"`
	PullRequest      bool   `json:"pull_request"`
}

type BuildPayload struct {
	ID     uint64 `json:"id"`
	Number string `json:"number"`
}

type RepositoryPayload struct {
	ID              uint64 `json:"id"`
	Slug            string `json:"slug"`
	GitHubID        uint64 `json:"github_id"`
	SourceURL       string `json:"source_url"`
	ApiURL          string `json:"api_url"`
	LastBuildID     uint64 `json:"last_build_id"`
	LastBuildNumber string `json:"last_build_number"`
	Description     string `json:"description"`
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

	Started() error
	Error(context.Context, string) error
	Requeue() error
	Finish(FinishState) error

	LogWriter(context.Context) (io.WriteCloser, error)
}
