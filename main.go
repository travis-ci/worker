package main

import (
	"fmt"
)

func main() {
	config, err := ConfigFromFile("config/worker.json")
	if err != nil {
		fmt.Printf("Error reading config: %v\n", err)
		return
	}

	jobChan := make(chan Job, 2)

	worker1 := NewWorker("go-worker-1", NewBlueBox(config.BlueBox), jobChan)
	worker2 := NewWorker("go-worker-2", NewBlueBox(config.BlueBox), jobChan)

	jobChan <- Job{
		Id:          1,
		BuildScript: []byte("echo This is build 1"),
	}
	jobChan <- Job{
		Id:          2,
		BuildScript: []byte("echo This is build 2"),
	}
	close(jobChan)

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
