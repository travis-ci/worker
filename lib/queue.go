package lib

import (
	"encoding/json"
)

// A JobQueue pulls jobs off an AMQP queue.
type JobQueue struct {
	mb        MessageBroker
	queueName string
	subCount  int
}

type JobPayloadProcessor interface {
	Process(payload Payload)
}

type jobPayloadMessageProcessor struct {
	payloadProcessor JobPayloadProcessor
}

// A Payload holds the information necessary to run the job.
type Payload struct {
	Type       string                 `json:"type"`
	Job        JobPayload             `json:"job"`
	BuildJob   JobPayload             `json:"build"`
	Build      BuildPayload           `json:"source"`
	Repository RepositoryPayload      `json:"repository"`
	Queue      string                 `json:"queue"`
	UUID       string                 `json:"uuid"`
	Config     map[string]interface{} `json:"config"`
}

// A JobPayload holds the information specific to the job.
type JobPayload struct {
	ID               int64  `json:"id"`
	Number           string `json:"number"`
	Commit           string `json:"commit"`
	CommitRange      string `json:"commit_range"`
	Branch           string `json:"branch"`
	Ref              string `json:"ref"`
	State            string `json:"state"`
	SecureEnvEnabled bool   `json:"secure_env_enabled"`
}

// A BuildPayload holds the information specific to the build.
type BuildPayload struct {
	ID     int64  `json:"id"`
	Number string `json:"number"`
}

// A RepositoryPayload holds the information specific to the repository.
type RepositoryPayload struct {
	ID        int64  `json:"id"`
	Slug      string `json:"slug"`
	GitHubID  int64  `json:"github_id"`
	SourceURL string `json:"source_url"`
	APIURL    string `json:"api_url"`
}

// NewQueue creates a new JobQueue. The name is the name of the queue to
// subscribe to, and the subCount is the number of jobs that can be fetched at
// once before having to Ack.
func NewQueue(mb MessageBroker, queueName string, subCount int) *JobQueue {
	return &JobQueue{mb, queueName, subCount}
}

func (q *JobQueue) Subscribe(gracefulQuitChan chan bool, f func() JobPayloadProcessor) error {
	return q.mb.Subscribe(q.queueName, q.subCount, gracefulQuitChan, func() MessageProcessor {
		return &jobPayloadMessageProcessor{f()}
	})
}

func (mp *jobPayloadMessageProcessor) Process(message []byte) {
	var payload Payload
	json.Unmarshal(message, &payload)
	mp.payloadProcessor.Process(payload)
	return
}
