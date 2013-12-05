package main

import (
	"encoding/json"
	"github.com/streadway/amqp"
	"io"
	"time"
)

// A Reporter is used to report back to the rest of the Travis CI system about
// the job.
type Reporter struct {
	channel        *amqp.Channel
	numberSequence chan int
	done           chan bool
	jobID          int64
	Log            io.WriteCloser
}

// NewReporter creates a new reporter for the given job ID. The reporter is only
// meant to be used for a single job.
func NewReporter(config AMQPConfig, jobID int64) (*Reporter, error) {
	conn, err := amqp.Dial(config.URL)
	if err != nil {
		return nil, err
	}

	channel, err := conn.Channel()
	if err != nil {
		return nil, err
	}

	err = channel.ExchangeDeclare("reporting", "topic", true, false, false, false, nil)
	if err != nil {
		return nil, err
	}

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
		channel:        channel,
		jobID:          jobID,
		done:           done,
		numberSequence: sequence,
	}
	reporter.Log = &writeCloseWrapper{reporter}

	return reporter, nil
}

type writeCloseWrapper struct {
	reporter *Reporter
}

func (w *writeCloseWrapper) Write(p []byte) (n int, err error) {
	str := string(p)
	return len(str), w.reporter.publishLogPart(logPart{
		ID:     w.reporter.jobID,
		Log:    str,
		Number: w.reporter.nextPartNumber(),
		Final:  false,
	})
}

func (w *writeCloseWrapper) Close() error {
	return w.reporter.publishLogPart(logPart{
		ID:     w.reporter.jobID,
		Log:    "",
		Number: w.reporter.nextPartNumber(),
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

	msg := amqp.Publishing{
		Type:      "job:test:log",
		Timestamp: time.Now(),
		Body:      data,
	}

	return r.channel.Publish("reporting", "reporting.jobs.logs", false, false, msg)
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

	msg := amqp.Publishing{
		Type:         event,
		Timestamp:    time.Now(),
		ContentType:  "application/json",
		Body:         data,
		DeliveryMode: amqp.Transient,
	}

	return r.channel.Publish("reporting", "reporting.jobs.builds", false, false, msg)
}

// Close closes the reporter. This should be called after the job has finished.
func (r *Reporter) Close() error {
	return r.channel.Close()
}

func (r *Reporter) nextPartNumber() int {
	return <-r.numberSequence
}

func currentJSONTime() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}
