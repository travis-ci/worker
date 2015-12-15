package config

import (
	"os"
	"time"
)

var (
	defaultAmqpURI                = "amqp://"
	defaultBaseDir                = "."
	defaultFilePollingInterval, _ = time.ParseDuration("5s")
	defaultPoolSize               = 1
	defaultProviderName           = "docker"
	defaultQueueType              = "amqp"

	defaultHardTimeout, _            = time.ParseDuration("50m")
	defaultLogTimeout, _             = time.ParseDuration("10m")
	defaultScriptUploadTimeout, _    = time.ParseDuration("3m30s")
	defaultStartupTimeout, _         = time.ParseDuration("4m")
	defaultBuildCacheFetchTimeout, _ = time.ParseDuration("5m")
	defaultBuildCachePushTimeout, _  = time.ParseDuration("5m")

	defaultHostname, _ = os.Hostname()
	defaultLanguage    = "default"
	defaultDist        = "precise"
	defaultGroup       = "stable"
	defaultOS          = "linux"
)

func init() {
	wd, err := os.Getwd()
	if err != nil {
		return
	}

	defaultBaseDir = wd
}
