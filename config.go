package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// WorkerConfig holds the configuration for travis-worker.
type WorkerConfig struct {
	Name         string
	WorkerCount  int
	LogTimestamp string
	Provider     string
	BlueBox      BlueBoxConfig
	SauceLabs    SauceLabsConfig
	AMQP         AMQPConfig
	Timeouts     TimeoutsConfig
	LogLimits    LogLimitsConfig
	Librato      LibratoConfig
}

// BlueBoxConfig holds the configuration relevant to connecting to the Blue Box
// API.
type BlueBoxConfig struct {
	CustomerID string
	APIKey     string
	LocationID string
	ProductID  string
	IPv6Only   bool
}

// SauceLabsConfig holds the configuration relevant to connecting to the Sauce
// Labs Mac VM API.
type SauceLabsConfig struct {
	Endpoint         string
	SSHKeyPath       string
	SSHKeyPassphrase string
	ImageName        string
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

type LibratoConfig struct {
	Email string
	Token string
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

// Validate checks the config it is run on and returns any errors (such as
// missing fields).
func (c WorkerConfig) Validate() (errors []error) {
	if c.Name == "" {
		errors = append(errors, fmt.Errorf("config: name is required"))
	}
	if c.WorkerCount == 0 {
		errors = append(errors, fmt.Errorf("config: workerCount must be at least 1"))
	}
	switch c.Provider {
	case "blueBox":
		if c.BlueBox == (BlueBoxConfig{}) {
			errors = append(errors, fmt.Errorf("config: blueBox config must be specified when provider is blueBox"))
		} else {
			errors = append(errors, c.validateBlueBox()...)
		}
	case "sauceLabs":
		if c.SauceLabs == (SauceLabsConfig{}) {
			errors = append(errors, fmt.Errorf("config: sauceLabs config must be specified when provider is sauceLabs"))
		} else {
			errors = append(errors, c.validateSauceLabs()...)
		}
	case "":
		errors = append(errors, fmt.Errorf("config: provider is required"))
	default:
		errors = append(errors, fmt.Errorf("config: unknown provider %s", c.Provider))
	}
	if c.AMQP.URL == "" {
		errors = append(errors, fmt.Errorf("config: AMQP URL is required"))
	}
	if c.AMQP.Queue == "" {
		errors = append(errors, fmt.Errorf("config: AMQP queue is required"))
	}

	return
}

func (c WorkerConfig) validateBlueBox() (errors []error) {
	if c.BlueBox.CustomerID == "" {
		errors = append(errors, fmt.Errorf("config: blueBox: customer ID is required"))
	}
	if c.BlueBox.APIKey == "" {
		errors = append(errors, fmt.Errorf("config: blueBox: API key is required"))
	}
	if c.BlueBox.LocationID == "" {
		errors = append(errors, fmt.Errorf("config: blueBox: location ID is required"))
	}
	if c.BlueBox.ProductID == "" {
		errors = append(errors, fmt.Errorf("config: blueBox: product ID is required"))
	}

	return
}

func (c WorkerConfig) validateSauceLabs() (errors []error) {
	if c.SauceLabs.Endpoint == "" {
		errors = append(errors, fmt.Errorf("config: sauceLabs: endpoint is required"))
	}
	if c.SauceLabs.SSHKeyPath == "" {
		errors = append(errors, fmt.Errorf("config: sauceLabs: SSH key path is required"))
	}
	if c.SauceLabs.SSHKeyPassphrase == "" {
		errors = append(errors, fmt.Errorf("config: sauceLabs: SSH key passphrase is required"))
	}
	if c.SauceLabs.ImageName == "" {
		errors = append(errors, fmt.Errorf("config: sauceLabs: image name is required"))
	}

	return
}
