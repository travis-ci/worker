package main

import (
	"encoding/json"
	"github.com/streadway/amqp"
	"log"
)

// A JobQueue pulls jobs off an AMQP queue.
type JobQueue struct {
	conn           *amqp.Connection
	channel        *amqp.Channel
	queue          amqp.Queue
	payloadChannel chan Payload
	doneChannel    chan error
}

// A Payload holds the information necessary to run the job.
type Payload struct {
	Job        JobPayload
	Build      BuildPayload `json:"source"`
	Repository RepositoryPayload
	Queue      string
	UUID       string

	delivery amqp.Delivery
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

// Finish notifies the queue that the job finished. If the passed bool is true,
// the job will be requeued.
func (p Payload) Finish(requeue bool) error {
	if requeue {
		return p.delivery.Nack(false, true)
	} else {
		return p.delivery.Ack(false)
	}
}

// NewQueue creates a new JobQueue. The name is the name of the queue to
// subscribe to, and the size is the number of jobs that can be fetched at once
// before having to Ack.
func NewQueue(conn *amqp.Connection, name string, size int) (*JobQueue, error) {
	var err error
	queue := &JobQueue{conn: conn, doneChannel: make(chan error)}

	queue.channel, err = queue.conn.Channel()
	if err != nil {
		return nil, err
	}

	err = queue.channel.Qos(size, 0, false)
	if err != nil {
		return nil, err
	}

	queue.queue, err = queue.channel.QueueDeclare(
		name,  // name of the queue
		true,  // durable
		false, // delete when unused
		false, // exclusive
		false, // nowait
		nil,   // arguments
	)
	if err != nil {
		return nil, err
	}

	queue.payloadChannel = make(chan Payload)

	deliveries, err := queue.channel.Consume(name, generatePassword(), false, false, false, false, nil)
	if err != nil {
		return nil, err
	}
	go handle(deliveries, queue.payloadChannel, queue.doneChannel)

	return queue, nil
}

// PayloadChannel returns a channel which Payloads can be read off of.
func (q *JobQueue) PayloadChannel() chan Payload {
	return q.payloadChannel
}

func (q *JobQueue) publish(p Payload) error {
	body, _ := json.Marshal(p)
	msg := amqp.Publishing{
		Type:        "test",
		ContentType: "application/json",
		Body:        body,
	}
	return q.channel.Publish("", p.Queue, false, false, msg)
}

// Shutdown closes the AMQP channel and the PayloadChannel
func (q *JobQueue) Shutdown() error {
	if err := q.channel.Close(); err != nil {
		return err
	}

	return <-q.doneChannel
}

func handle(deliveries <-chan amqp.Delivery, payloads chan Payload, done chan error) {
	for d := range deliveries {
		payloads <- deliveryToPayload(d)
	}
	close(payloads)
	done <- nil
}

func deliveryToPayload(delivery amqp.Delivery) Payload {
	var payload Payload
	err := json.Unmarshal(delivery.Body, &payload)
	if err != nil {
		log.Printf("Error occurred while parsing AMQP delivery: %v", err)
	}

	payload.delivery = delivery

	return payload
}
