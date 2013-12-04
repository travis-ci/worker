package main

import (
	"fmt"
	"github.com/streadway/amqp"
	"log"
)

func main() {
	config, err := ConfigFromFile("config/worker.json")
	if err != nil {
		fmt.Printf("Error reading config: %v\n", err)
		return
	}

	amqpConn, err := amqp.Dial(config.AMQP.URL)
	if err != nil {
		fmt.Printf("Error connecting to AMQP: %v\n", err)
		return
	}
	defer amqpConn.Close()

	queue1, err := NewQueue(amqpConn, config.AMQP.Queue)
	if err != nil {
		fmt.Printf("Couldn't create queue1: %v\n", err)
	}
	queue2, err := NewQueue(amqpConn, config.AMQP.Queue)
	if err != nil {
		fmt.Printf("Couldn't create queue2: %v\n", err)
	}
	reporter1, err := NewReporter(amqpConn)
	if err != nil {
		fmt.Printf("Couldn't create reporter1: %v\n", err)
	}
	reporter2, err := NewReporter(amqpConn)
	if err != nil {
		fmt.Printf("Couldn't create reporter2: %v\n", err)
	}
	worker1 := NewWorker("go-worker-1", NewBlueBox(config.BlueBox), queue1, reporter1)
	worker2 := NewWorker("go-worker-2", NewBlueBox(config.BlueBox), queue2, reporter2)

	doneChan := make(chan bool)
	go func() {
		worker1.Start()
		doneChan <- true
	}()
	go func() {
		worker2.Start()
		doneChan <- true
	}()

	<-doneChan
	<-doneChan
}
