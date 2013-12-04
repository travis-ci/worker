package main

import (
	"encoding/json"
	"os"
)

type WorkerConfig struct {
	BlueBox BlueBoxConfig
	AMQP    AMQPConfig
}

type BlueBoxConfig struct {
	CustomerId string
	ApiKey     string
	LocationId string
	TemplateId string
	ProductId  string
}

type AMQPConfig struct {
	URL   string
	Queue string
}

func ConfigFromFile(fileName string) (c WorkerConfig, err error) {
	file, err := os.Open(fileName)
	if err != nil {
		return
	}

	decoder := json.NewDecoder(file)
	err = decoder.Decode(&c)
	return
}
