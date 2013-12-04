package main

import (
	"encoding/json"
	"github.com/streadway/amqp"
	"time"
)

type Reporter struct {
	channel        *amqp.Channel
	numberSequence chan int
	done           chan bool
	jobId          int64
}

func NewReporter(conn *amqp.Connection, jobId int64) (*Reporter, error) {
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
		var number int = 1
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

	return &Reporter{
		channel:        channel,
		jobId:          jobId,
		done:           done,
		numberSequence: sequence,
	}, nil
}

type logPart struct {
	Id     int64  `json:"id"`
	Log    string `json:"log"`
	Number int    `json:"number"`
	Final  bool   `json:"final,omitempty"`
}

func (r *Reporter) SendLog(output string) error {
	return r.publishLogPart(logPart{
		Id:     r.jobId,
		Log:    output,
		Number: r.nextPartNumber(),
		Final:  false,
	})
}

func (r *Reporter) SendFinal() error {
	return r.publishLogPart(logPart{
		Id:     r.jobId,
		Log:    "",
		Number: r.nextPartNumber(),
		Final:  true,
	})
}

type jobReporterPayload struct {
	Id         int64  `json:"id"`
	State      string `json:"state"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

func (r *Reporter) NotifyJobStarted() error {
	return r.notify("job:test:start", jobReporterPayload{Id: r.jobId, State: "started", StartedAt: currentJsonTime()})
}

func (r *Reporter) NotifyJobFinished(state string) error {
	return r.notify("job:test:finish", jobReporterPayload{Id: r.jobId, State: state, FinishedAt: currentJsonTime()})
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

func (r *Reporter) Close() error {
	return r.channel.Close()
}

func (r *Reporter) nextPartNumber() int {
	return <-r.numberSequence
}

func currentJsonTime() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}
