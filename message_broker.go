package main

import (
	"fmt"
	"github.com/streadway/amqp"
	"sync"
	"time"
)

type MessageBroker interface {
	DeclareQueue(string) error
	Subscribe(string, int, chan bool, func() MessageProcessor) error
	SubscribeFanout(string, func() MessageProcessor) error
	Publish(string, string, string, []byte) error
	Close() error
}

type MessageProcessor interface {
	Process(message []byte)
}

type RabbitMessageBroker struct {
	conn        *amqp.Connection
	publishChan *amqp.Channel
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
	msg := amqp.Publishing{
		Type:      msgType,
		Timestamp: time.Now(),
		Body:      message,
	}

	return mb.publishChan.Publish(exchange, routingKey, false, false, msg)
}

// Subscribe will start pulling messages off the given queue and process up to
// subCount messages concurrently by passing them to the given function.
//
// When the passed gracefulQuitChan is closed, the subscribers shut down after
// finishing the message it is currently processing.
func (mb *RabbitMessageBroker) Subscribe(queueName string, subCount int, gracefulQuitChan chan bool, f func() MessageProcessor) error {
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
		go func() {
			defer wg.Done()

			for {
				select {
				case <-gracefulQuitChan:
					return
				case message, ok := <-messages:
					if !ok {
						return
					}

					f().Process(message.Body)
					message.Ack(false)
				}
			}
		}()
	}
	wg.Wait()

	return nil
}

func (mb *RabbitMessageBroker) SubscribeFanout(exchange string, f func() MessageProcessor) error {
	ch, err := mb.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	err = ch.Qos(1, 0, false)
	if err != nil {
		return err
	}

	err = ch.ExchangeDeclare(exchange, "fanout", false, false, false, false, nil)
	if err != nil {
		return err
	}

	queue, err := ch.QueueDeclare("", false, false, true, false, nil)
	if err != nil {
		return err
	}

	err = ch.QueueBind(queue.Name, "", exchange, false, nil)
	if err != nil {
		return err
	}

	messages, err := ch.Consume(queue.Name, "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	for message := range messages {
		f().Process(message.Body)
		message.Ack(false)
	}

	return nil
}

func (mb *RabbitMessageBroker) Close() error {
	err := mb.publishChan.Close()
	if err != nil {
		return err
	}

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

	publishChan, err := conn.Channel()
	if err != nil {
		return nil, err
	}

	return &RabbitMessageBroker{conn, publishChan}, nil
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

func (mb *TestMessageBroker) Subscribe(queueName string, subCount int, gracefulQuitChan chan bool, f func() MessageProcessor) error {
	var wg sync.WaitGroup
	wg.Add(subCount)
	for i := 0; i < subCount; i++ {
		go func() {
			defer wg.Done()

			processor := f()

			for body := range mb.queues[queueName] {
				processor.Process(body)
			}
		}()
	}
	wg.Wait()

	return nil
}

func (mb *TestMessageBroker) SubscribeFanout(exchange string, f func() MessageProcessor) error {
	return fmt.Errorf("TestMessageBroker.SubscribeFanout needs to be implemented")
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
