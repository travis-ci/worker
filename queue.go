package main

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
	Process(payload Payload) error
}

type jobPayloadMessageProcessor struct {
	payloadProcessor JobPayloadProcessor
}

// A Payload holds the information necessary to run the job.
type Payload struct {
	Job        JobPayload
	Build      BuildPayload `json:"source"`
	Repository RepositoryPayload
	Queue      string
	UUID       string
}

// A JobPayload holds the information specific to the job.
type JobPayload struct {
	ID               int64
	Number           string
	Commit           string
	CommitRange      string `json:"commit_range"`
	Branch           string
	Ref              string
	State            string
	SecureEnvEnabled bool `json:"secure_env_enabled"`
}

// A BuildPayload holds the information specific to the build.
type BuildPayload struct {
	ID     int64
	Number string
}

// A RepositoryPayload holds the information specific to the repository.
type RepositoryPayload struct {
	ID        int64
	Slug      string
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

func (q *JobQueue) Subscribe(f func() JobPayloadProcessor) error {
	return q.mb.Subscribe(q.queueName, q.subCount, func() MessageProcessor {
		return &jobPayloadMessageProcessor{f()}
	})
}

func (mp *jobPayloadMessageProcessor) Process(message []byte) error {
	var payload Payload
	json.Unmarshal(message, &payload)
	return mp.payloadProcessor.Process(payload)
}
