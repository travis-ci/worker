package main

import (
	"bytes"
	"encoding/json"
	"io"
	"time"
)

// A Reporter is used to report back to the rest of the Travis CI system about
// the job.
type Reporter struct {
	mb             MessageBroker
	numberSequence chan int
	done           chan bool
	jobID          int64
	Log            io.WriteCloser
}

// NewReporter creates a new reporter for the given job ID. The reporter is only
// meant to be used for a single job.
func NewReporter(mb MessageBroker, jobID int64) (*Reporter, error) {
	done := make(chan bool)
	sequence := make(chan int)
	go func() {
		number := 1
		for {
			select {
			case <-done:
				close(sequence)
				return
			case sequence <- number:
				number++
			}
		}
	}()

	reporter := &Reporter{
		mb:             mb,
		jobID:          jobID,
		done:           done,
		numberSequence: sequence,
	}
	reporter.Log = NewCoalesceWriteCloser(reporter)

	return reporter, nil
}

func (r *Reporter) Write(p []byte) (n int, err error) {
	str := string(bytes.Replace(p, []byte{0}, []byte{}, -1))
	if len(str) == 0 {
		return len(p), nil
	}

	return len(p), r.publishLogPart(logPart{
		ID:     r.jobID,
		Log:    str,
		Number: r.nextPartNumber(),
		Final:  false,
	})
}

func (r *Reporter) Close() error {
	return r.publishLogPart(logPart{
		ID:     r.jobID,
		Log:    "",
		Number: r.nextPartNumber(),
		Final:  true,
	})
}

type logPart struct {
	ID     int64  `json:"id"`
	Log    string `json:"log"`
	Number int    `json:"number"`
	Final  bool   `json:"final,omitempty"`
}

func (r *Reporter) publishLogPart(part logPart) error {
	data, err := json.Marshal(part)
	if err != nil {
		return err
	}

	return r.mb.Publish("reporting", "reporting.jobs.logs", "job:test:log", data)
}

type jobReporterPayload struct {
	ID         int64  `json:"id"`
	State      string `json:"state"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// NotifyJobStarted notifies that the job has started and starts the duration
// timer for the job.
func (r *Reporter) NotifyJobStarted() error {
	return r.notify("job:test:start", jobReporterPayload{ID: r.jobID, State: "started", StartedAt: currentJSONTime()})
}

// NotifyJobFinished notifies that the job finished with the given state and
// stops the duration timer.
func (r *Reporter) NotifyJobFinished(state string) error {
	return r.notify("job:test:finish", jobReporterPayload{ID: r.jobID, State: state, FinishedAt: currentJSONTime()})
}

func (r *Reporter) notify(event string, payload jobReporterPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return r.mb.Publish("reporting", "reporting.jobs.builds", event, data)
}

func (r *Reporter) nextPartNumber() int {
	return <-r.numberSequence
}

func currentJSONTime() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}
