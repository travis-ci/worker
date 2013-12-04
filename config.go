package main

import (
	"encoding/json"
	"os"
)

// WorkerConfig holds the configuration for travis-worker.
type WorkerConfig struct {
	BlueBox BlueBoxConfig
	AMQP    AMQPConfig
}

// BlueBoxConfig holds the configuration relevant to connecting to the Blue Box
// API.
type BlueBoxConfig struct {
	CustomerID string
	APIKey     string
	LocationID string
	TemplateID string
	ProductID  string
}

// AMQPConfig holds the configuration values relevant to communicating with the
// AMQP server.
type AMQPConfig struct {
	URL   string
	Queue string
}

// ConfigFromFile opens the named JSON configuration file and parses the
// configuration in it. An error could be returned if there was a problem
// reading the file or parsing the JSON object inside of it.
func ConfigFromFile(fileName string) (c WorkerConfig, err error) {
	file, err := os.Open(fileName)
	if err != nil {
		return
	}

	decoder := json.NewDecoder(file)
	err = decoder.Decode(&c)
	return
}
