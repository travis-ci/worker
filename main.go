package main

import (
	"fmt"
	"io/ioutil"
)

func main() {
	config, err := ConfigFromFile("config/worker.json")
	if err != nil {
		fmt.Printf("Error reading config: %v\n", err)
		return
	}

	api := NewBlueBox(config.BlueBox)

	jobChan := make(chan Job, 1)

	worker := NewWorker("go-worker-1", api, jobChan)

	job := Job{
		Id: 1,
	}
	job.BuildScript, _ = ioutil.ReadFile("build.sh")
	jobChan <- job
	close(jobChan)

	worker.Start()
}
