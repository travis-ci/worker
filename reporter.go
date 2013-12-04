package main

import (
	"encoding/json"
	"github.com/streadway/amqp"
	"sync"
	"time"
)

type Reporter struct {
	channel        *amqp.Channel
	logsPartNumber int64
	numberMutex    *sync.Mutex
}

func NewReporter(conn *amqp.Connection) (*Reporter, error) {
	channel, err := conn.Channel()
	if err != nil {
		return nil, err
	}

	err = channel.ExchangeDeclare("reporting", "topic", true, false, false, false, nil)
	if err != nil {
		return nil, err
	}

	return &Reporter{channel: channel, numberMutex: new(sync.Mutex)}, nil
}

type logPart struct {
	Id     int64  `json:"id"`
	Log    string `json:"log"`
	Number int64  `json:"number"`
	Final  bool   `json:"final,omitempty"`
}

func (r *Reporter) SendLog(jobId int64, output string) error {
	part := logPart{
		Id:     jobId,
		Log:    output,
		Number: r.nextPartNumber(),
		Final:  false,
	}
	return r.publishLogPart(part)
}

func (r *Reporter) SendFinal(jobId int64) error {
	part := logPart{
		Id:     jobId,
		Log:    "",
		Number: r.nextPartNumber(),
		Final:  true,
	}
	return r.publishLogPart(part)
}

type jobReporterPayload struct {
	Id         int64  `json:"id"`
	State      string `json:"state"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

func (r *Reporter) NotifyJobStarted(jobId int64) error {
	return r.notify("job:test:start", jobReporterPayload{Id: jobId, State: "started", StartedAt: currentJsonTime()})
}

func (r *Reporter) NotifyJobFinished(jobId int64, state string) error {
	return r.notify("job:test:finish", jobReporterPayload{Id: jobId, State: state, FinishedAt: currentJsonTime()})
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

func (r *Reporter) nextPartNumber() int64 {
	r.numberMutex.Lock()
	defer r.numberMutex.Unlock()
	r.logsPartNumber += 1
	return r.logsPartNumber
}

func currentJsonTime() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}
