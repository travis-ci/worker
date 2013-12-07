package main

import (
	"encoding/json"
	"os"
)

// WorkerConfig holds the configuration for travis-worker.
type WorkerConfig struct {
	BlueBox   BlueBoxConfig
	AMQP      AMQPConfig
	Timeouts  TimeoutsConfig
	LogLimits LogLimitsConfig
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

// TimeoutsConfig holds the different timeouts that can occur. All timeouts are
// given in seconds.
type TimeoutsConfig struct {
	HardLimit     int
	VMBoot        int
	LogInactivity int
}

type LogLimitsConfig struct {
	// The maximum log length, given in bytes
	MaxLogLength int64

	// The maximum length of all log chunks being sent back to RabbitMQ, in
	// bytes
	LogChunkSize int
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
