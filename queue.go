package main

import (
	"encoding/json"
	"github.com/streadway/amqp"
	"log"
)

type JobQueue struct {
	conn           *amqp.Connection
	channel        *amqp.Channel
	queue          amqp.Queue
	payloadChannel chan Payload
	doneChannel    chan error
}

type Payload struct {
	Job        JobPayload
	Build      BuildPayload `json:"source"`
	Repository RepositoryPayload
	Queue      string
	UUID       string

	delivery amqp.Delivery
}

type JobPayload struct {
	Id               int64
	Number           string
	Commit           string
	CommitRange      string `json:"commit_range"`
	Branch           string
	Ref              string
	State            string
	SecureEnvEnabled bool `json:"secure_env_enabled"`
}

type BuildPayload struct {
	Id     int64
	Number string
}

type RepositoryPayload struct {
	Id        int64
	Slug      string
	GitHubId  int64  `json:"github_id"`
	SourceURL string `json:"source_url"`
	APIURL    string `json:"api_url"`
}

func (p Payload) Ack() error {
	return p.delivery.Ack(false)
}

func (p Payload) Nack() error {
	return p.delivery.Nack(false, true)
}

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

func (q *JobQueue) PayloadChannel() chan Payload {
	return q.payloadChannel
}

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
