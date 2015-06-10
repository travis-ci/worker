package worker

import (
	"fmt"
	"strings"

	"github.com/codegangsta/cli"
)

var (
	Flags = []cli.Flag{
		cli.StringFlag{Name: "amqp-uri", EnvVar: twEnvVars("AMQP_URI")},
		cli.IntFlag{Name: "pool-size", EnvVar: twEnvVars("POOL_SIZE")},
		cli.StringFlag{Name: "build-api-uri", EnvVar: twEnvVars("BUILD_API_URI")},
		cli.StringFlag{Name: "provider-name", EnvVar: twEnvVars("PROVIDER_NAME")},
		cli.StringFlag{Name: "queue-name", EnvVar: twEnvVars("QUEUE_NAME")},
		cli.StringFlag{Name: "librato-email", EnvVar: twEnvVars("LIBRATO_EMAIL")},
		cli.StringFlag{Name: "librato-token", EnvVar: twEnvVars("LIBRATO_TOKEN")},
		cli.StringFlag{Name: "librato-source", EnvVar: twEnvVars("LIBRATO_SOURCE")},
		cli.StringFlag{Name: "sentry-dsn", EnvVar: twEnvVars("SENTRY_DSN")},
		cli.StringFlag{Name: "hostname", EnvVar: twEnvVars("HOSTNAME")},
		cli.IntFlag{Name: "hard-timeout-seconds", EnvVar: twEnvVars("HARD_TIMEOUT_SECONDS")},
		cli.StringFlag{Name: "skip-shutdown-on-log-timeout", EnvVar: twEnvVars("SKIP_SHUTDOWN_ON_LOG_TIMEOUT")},
	}
)

func twEnvVars(key string) string {
	return strings.ToUpper(fmt.Sprintf("TRAVIS_WORKER_%s,%s", key, key))
}

// Config contains all the configuration needed to run the worker.
type Config struct {
	AmqpURI            string
	PoolSize           int
	BuildAPIURI        string
	ProviderName       string
	QueueName          string
	LibratoEmail       string
	LibratoToken       string
	LibratoSource      string
	SentryDSN          string
	Hostname           string
	HardTimeoutSeconds int

	SkipShutdownOnLogTimeout string
}

func ConfigFromCLIContext(cfg *Config, c *cli.Context) *Config {
	return cfg
}
