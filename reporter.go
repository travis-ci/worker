package main

import (
	"bytes"
	"encoding/json"
	"time"
)

// A StateUpdater is used to update the state of a job.
type StateUpdater struct {
	mb    MessageBroker
	jobID int64
}

// NewStateUpdater returns a new StateUpdater that updates the state for the
// job with the given job ID.
func NewStateUpdater(mb MessageBroker, jobID int64) *StateUpdater {
	return &StateUpdater{mb, jobID}
}

type jobReporterPayload struct {
	ID         int64  `json:"id"`
	State      string `json:"state"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// Start marks the job as started and start the duration counter.
func (su *StateUpdater) Start() error {
	return su.notify("job:test:start", jobReporterPayload{ID: su.jobID, State: "started", StartedAt: currentJSONTime()})
}

// Finish marks the job as finished and stop the duration counter. The given state
// should indicate the result of the job, such as 'passed', 'failed', etc.
func (su *StateUpdater) Finish(state string) error {
	return su.notify("job:test:finish", jobReporterPayload{ID: su.jobID, State: state, FinishedAt: currentJSONTime()})
}

// Reset resets the job and requeues it.
func (su *StateUpdater) Reset() error {
	return su.notify("job:test:reset", jobReporterPayload{ID: su.jobID, State: "reset", FinishedAt: currentJSONTime()})
}

func (su *StateUpdater) notify(event string, payload jobReporterPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return su.mb.Publish("reporting", "reporting.jobs.builds", event, data)
}

func currentJSONTime() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

// A LogWriter acts as an io.WriteCloser that forwards its writes to
// travis-logs.
type LogWriter struct {
	mb                    MessageBroker
	jobID                 int64
	logPartNumberSequence chan int
}

type logPart struct {
	ID     int64  `json:"id"`
	Log    string `json:"log"`
	Number int    `json:"number"`
	Final  bool   `json:"final,omitempty"`
}

// NewLogWriter returns a new LogWriter that writes to the log for the job with
// the given job ID.
func NewLogWriter(mb MessageBroker, jobID int64) *LogWriter {
	sequence := make(chan int)
	go func() {
		number := 1
		for {
			sequence <- number
			number++
		}
	}()

	return &LogWriter{mb, jobID, sequence}
}

func (lw *LogWriter) Write(p []byte) (n int, err error) {
	str := string(bytes.Replace(p, []byte{0}, []byte{}, -1))
	if len(str) == 0 {
		return len(p), nil
	}

	return len(p), lw.publishLogPart(logPart{
		ID:     lw.jobID,
		Log:    str,
		Number: lw.nextPartNumber(),
		Final:  false,
	})
}

func (lw *LogWriter) Close() error {
	return lw.publishLogPart(logPart{
		ID:     lw.jobID,
		Number: lw.nextPartNumber(),
		Final:  true,
	})
}

func (lw *LogWriter) publishLogPart(part logPart) error {
	data, err := json.Marshal(part)
	if err != nil {
		return err
	}

	return lw.mb.Publish("reporting", "reporting.jobs.logs", "job:test:log", data)
}

func (lw *LogWriter) nextPartNumber() int {
	return <-lw.logPartNumberSequence
}
