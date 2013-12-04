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

func NewQueue(conn *amqp.Connection, name string) (*JobQueue, error) {
	var err error
	queue := &JobQueue{conn: conn}

	queue.channel, err = queue.conn.Channel()
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
	go handle(deliveries, queue.payloadChannel)

	return queue, nil
}

func (q *JobQueue) PayloadChannel() chan Payload {
	return q.payloadChannel
}

func (q *JobQueue) Shutdown() error {
	if err := q.conn.Close(); err != nil {
		return err
	}

	return nil
}

func handle(deliveries <-chan amqp.Delivery, payloads chan Payload) {
	for d := range deliveries {
		payloads <- deliveryToPayload(d)
	}
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
