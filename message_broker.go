package main

import (
	"fmt"
	"github.com/streadway/amqp"
	"sync"
	"time"
)

type MessageBroker interface {
	DeclareQueue(string) error
	Subscribe(string, int, func(int) MessageProcessor) error
	Publish(string, string, string, []byte) error
	Close() error
}

type MessageProcessor interface {
	Process(message []byte) error
}

type RabbitMessageBroker struct {
	conn *amqp.Connection
}

type TestMessageBroker struct {
	queues map[string]chan []byte
}

func (mb *RabbitMessageBroker) DeclareQueue(queueName string) error {
	ch, err := mb.conn.Channel()
	if err != nil {
		return err
	}

	_, err = ch.QueueDeclare(queueName, true, false, false, false, nil)
	return err
}

func (mb *RabbitMessageBroker) Publish(exchange, routingKey, msgType string, message []byte) error {
	ch, err := mb.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	msg := amqp.Publishing{
		Type:      msgType,
		Timestamp: time.Now(),
		Body:      message,
	}

	return ch.Publish(exchange, routingKey, false, false, msg)
}

func (mb *RabbitMessageBroker) Subscribe(queueName string, subCount int, f func(int) MessageProcessor) error {
	ch, err := mb.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	err = ch.Qos(subCount, 0, false)
	if err != nil {
		return err
	}

	messages, err := ch.Consume(queueName, "processor", false, false, false, false, nil)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	wg.Add(subCount)
	for i := 0; i < subCount; i++ {
		go func(messageProcessorNum int) {
			defer wg.Done()

			for message := range messages {
				err := f(messageProcessorNum).Process(message.Body)
				if err == nil {
					message.Ack(false)
				} else {
					message.Nack(true, false)
				}
			}
		}(i)
	}
	wg.Wait()

	return nil
}

func (mb *RabbitMessageBroker) Close() error {
	return mb.conn.Close()
}

func NewMessageBroker(url string) (MessageBroker, error) {
	if url == "" {
		return nil, fmt.Errorf("URL is blank")
	}

	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, err
	}

	return &RabbitMessageBroker{conn}, nil
}

func NewTestMessageBroker() MessageBroker {
	return &TestMessageBroker{
		queues: make(map[string]chan []byte),
	}
}

func (mb *TestMessageBroker) DeclareQueue(queueName string) error {
	mb.queues[queueName] = make(chan []byte, 1)
	return nil
}

func (mb *TestMessageBroker) Subscribe(queueName string, subCount int, f func(int) MessageProcessor) error {
	var wg sync.WaitGroup
	wg.Add(subCount)
	for i := 0; i < subCount; i++ {
		go func(messageProcessorNum int) {
			defer wg.Done()

			processor := f(messageProcessorNum)

			for body := range mb.queues[queueName] {
				processor.Process(body)
			}
		}(i)
	}
	wg.Wait()

	return nil
}

func (mb *TestMessageBroker) Publish(exchange, routingKey, msgType string, msg []byte) error {
	mb.queues[routingKey] <- msg
	return nil
}

func (mb *TestMessageBroker) Close() error {
	for _, ch := range mb.queues {
		close(ch)
	}
	return nil
}
