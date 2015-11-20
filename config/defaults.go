package config

import (
	"os"
	"time"
)

var (
	defaultAmqpURI                   = "amqp://"
	defaultBaseDir                   = "."
	defaultFilePollingInterval, _    = time.ParseDuration("5s")
	defaultPoolSize                  = 1
	defaultProviderName              = "docker"
	defaultQueueType                 = "amqp"
	defaultHardTimeout, _            = time.ParseDuration("50m")
	defaultLogTimeout, _             = time.ParseDuration("10m")
	defaultBuildCacheFetchTimeout, _ = time.ParseDuration("5m")
	defaultBuildCachePushTimeout, _  = time.ParseDuration("5m")
	defaultHostname, _               = os.Hostname()
)

func init() {
	wd, err := os.Getwd()
	if err != nil {
		return
	}

	defaultBaseDir = wd
}
