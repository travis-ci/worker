package config

import "time"

var (
	defaultAmqpURI                   = "amqp://"
	defaultPoolSize                  = 1
	defaultProviderName              = "docker"
	defaultHardTimeout, _            = time.ParseDuration("50m")
	defaultBuildCacheFetchTimeout, _ = time.ParseDuration("5m")
	defaultBuildCachePushTimeout, _  = time.ParseDuration("5m")
)
