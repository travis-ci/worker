package main

import (
	"fmt"
	"github.com/streadway/amqp"
	"log"
	"os"
	"os/signal"
	"sync"
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

	queue, err := NewQueue(amqpConn, config.AMQP.Queue, 10)
	if err != nil {
		fmt.Printf("Couldn't create queue: %v\n", err)
	}

	sigtermChan := make(chan os.Signal, 1)
	signal.Notify(sigtermChan, os.Interrupt)

	var wg sync.WaitGroup

	for {
		select {
		case <-sigtermChan:
			log.Println("Got SIGTERM, shutting down workers gracefully")
			queue.Shutdown()
			wg.Wait()
			return
		case payload, ok := <-queue.PayloadChannel():
			if !ok {
				log.Printf("AMQP channel closed, stopping worker")
				queue.Shutdown()
				return
			}

			reporter, err := NewReporter(amqpConn, payload.Job.ID)
			if err != nil {
				log.Printf("Couldn't create reporter: %v\n", err)
				payload.Nack()
				break
			}

			worker := NewWorker(fmt.Sprintf("worker-%d", payload.Job.ID), NewBlueBox(config.BlueBox), payload, reporter)
			wg.Add(1)
			go func() {
				defer wg.Done()

				if worker.Run() {
					payload.Ack()
				} else {
					payload.Nack()
				}
			}()
		}
	}
}
