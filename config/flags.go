package config

import (
	"fmt"
	"strings"

	"github.com/codegangsta/cli"
)

var (
	Flags = []cli.Flag{
		cli.StringFlag{Name: "amqp-uri", Value: defaultAmqpURI, EnvVar: twEnvVars("AMQP_URI")},
		cli.IntFlag{Name: "pool-size", Value: defaultPoolSize, EnvVar: twEnvVars("POOL_SIZE")},
		cli.StringFlag{Name: "build-api-uri", EnvVar: twEnvVars("BUILD_API_URI")},
		cli.StringFlag{Name: "provider-name", Value: defaultProviderName, EnvVar: twEnvVars("PROVIDER_NAME")},
		cli.StringFlag{Name: "queue-name", EnvVar: twEnvVars("QUEUE_NAME")},
		cli.StringFlag{Name: "librato-email", EnvVar: twEnvVars("LIBRATO_EMAIL")},
		cli.StringFlag{Name: "librato-token", EnvVar: twEnvVars("LIBRATO_TOKEN")},
		cli.StringFlag{Name: "librato-source", EnvVar: twEnvVars("LIBRATO_SOURCE")},
		cli.StringFlag{Name: "sentry-dsn", EnvVar: twEnvVars("SENTRY_DSN")},
		cli.StringFlag{Name: "hostname", EnvVar: twEnvVars("HOSTNAME")},
		cli.DurationFlag{Name: "hard-timeout", Value: defaultHardTimeout, EnvVar: twEnvVars("HARD_TIMEOUT")},
		cli.StringFlag{Name: "skip-shutdown-on-log-timeout", EnvVar: twEnvVars("SKIP_SHUTDOWN_ON_LOG_TIMEOUT")},

		// build script generator flags
		cli.DurationFlag{Name: "build-cache-fetch-timeout", EnvVar: twEnvVars("BUILD_CACHE_FETCH_TIMEOUT")},
		cli.DurationFlag{Name: "build-cache-push-timeout", EnvVar: twEnvVars("BUILD_CACHE_PUSH_TIMEOUT")},
		cli.StringFlag{Name: "build-apt-cache", EnvVar: twEnvVars("BUILD_APT_CACHE")},
		cli.StringFlag{Name: "build-npm-cache", EnvVar: twEnvVars("BUILD_NPM_CACHE")},
		cli.BoolFlag{Name: "build-paranoid", EnvVar: twEnvVars("BUILD_PARANOID")},
		cli.BoolFlag{Name: "build-fix-resolv-conf", EnvVar: twEnvVars("BUILD_FIX_RESOLV_CONF")},
		cli.BoolFlag{Name: "build-fix-etc-hosts", EnvVar: twEnvVars("BUILD_FIX_ETC_HOSTS")},
		cli.StringFlag{Name: "build-cache-type", EnvVar: twEnvVars("BUILD_CACHE_TYPE")},
		cli.StringFlag{Name: "build-cache-s3-scheme", EnvVar: twEnvVars("BUILD_CACHE_S3_SCHEME")},
		cli.StringFlag{Name: "build-cache-s3-region", EnvVar: twEnvVars("BUILD_CACHE_S3_REGION")},
		cli.StringFlag{Name: "build-cache-s3-bucket", EnvVar: twEnvVars("BUILD_CACHE_S3_BUCKET")},
		cli.StringFlag{Name: "build-cache-s3-access-key-id", EnvVar: twEnvVars("BUILD_CACHE_S3_ACCESS_KEY_ID")},
		cli.StringFlag{Name: "build-cache-s3-secret-access-key", EnvVar: twEnvVars("BUILD_CACHE_S3_SECRET_ACCESS_KEY")},

		// non-config flags
		cli.StringFlag{Name: "pprof-port", Usage: "enable pprof http endpoint at port", EnvVar: twEnvVars("PPROF_PORT")},
		cli.BoolFlag{Name: "echo-config", Usage: "echo parsed config and exit", EnvVar: twEnvVars("ECHO_CONFIG")},
		cli.BoolFlag{Name: "debug", Usage: "set log level to debug", EnvVar: twEnvVars("DEBUG")},
	}
)

func twEnvVars(key string) string {
	return strings.ToUpper(fmt.Sprintf("TRAVIS_WORKER_%s,%s", key, key))
}
