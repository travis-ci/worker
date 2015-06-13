package config

import (
	"fmt"
	"strings"

	"github.com/codegangsta/cli"
)

var (
	Flags = []cli.Flag{
		cli.StringFlag{
			Name:   "amqp-uri",
			Value:  defaultAmqpURI,
			Usage:  "The URI to the AMQP server to connect to",
			EnvVar: twEnvVars("AMQP_URI"),
		},
		cli.IntFlag{
			Name:   "pool-size",
			Value:  defaultPoolSize,
			Usage:  "The size of the processor pool, affecting the number of jobs this worker can run in parallel",
			EnvVar: twEnvVars("POOL_SIZE"),
		},
		cli.StringFlag{
			Name:   "build-api-uri",
			Usage:  "The full URL to the build API endpoint to use. Note that this also requires the path of the URL. If a username is included in the URL, this will be translated to a token passed in the Authorization header",
			EnvVar: twEnvVars("BUILD_API_URI"),
		},
		cli.StringFlag{
			Name:   "provider-name",
			Value:  defaultProviderName,
			Usage:  "The name of the provider to use. See below for provider-specific configuration",
			EnvVar: twEnvVars("PROVIDER_NAME"),
		},
		cli.StringFlag{
			Name:   "queue-name",
			Usage:  "The AMQP queue to subscribe to for jobs",
			EnvVar: twEnvVars("QUEUE_NAME"),
		},
		cli.StringFlag{
			Name:   "librato-email",
			Usage:  "Librato metrics account email",
			EnvVar: twEnvVars("LIBRATO_EMAIL"),
		},
		cli.StringFlag{
			Name:   "librato-token",
			Usage:  "Librato metrics account token",
			EnvVar: twEnvVars("LIBRATO_TOKEN"),
		},
		cli.StringFlag{
			Name:   "librato-source",
			Usage:  "Librato metrics source name",
			EnvVar: twEnvVars("LIBRATO_SOURCE"),
		},
		cli.StringFlag{
			Name:   "sentry-dsn",
			Usage:  "The DSN to send Sentry events to",
			EnvVar: twEnvVars("SENTRY_DSN"),
		},
		cli.StringFlag{
			Name:   "hostname",
			Value:  defaultHostname,
			Usage:  "Host name used in log output to identify the source of a job",
			EnvVar: twEnvVars("HOSTNAME"),
		},
		cli.DurationFlag{
			Name:   "hard-timeout",
			Value:  defaultHardTimeout,
			Usage:  "The outermost (maximum) timeout for a given job, at which time the job is cancelled",
			EnvVar: twEnvVars("HARD_TIMEOUT"),
		},

		// build script generator flags
		cli.DurationFlag{
			Name:   "build-cache-fetch-timeout",
			Value:  defaultBuildCacheFetchTimeout,
			EnvVar: twEnvVars("BUILD_CACHE_FETCH_TIMEOUT"),
		},
		cli.DurationFlag{
			Name:   "build-cache-push-timeout",
			Value:  defaultBuildCachePushTimeout,
			EnvVar: twEnvVars("BUILD_CACHE_PUSH_TIMEOUT"),
		},
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

		// non-config and special case flags
		cli.StringFlag{
			Name:   "skip-shutdown-on-log-timeout",
			Usage:  "Special-case mode to aid with debugging timed out jobs",
			EnvVar: twEnvVars("SKIP_SHUTDOWN_ON_LOG_TIMEOUT"),
		},
		cli.BoolFlag{
			Name:   "build-api-insecure-skip-verify",
			Usage:  "Skip build API TLS verification (useful for Enterprise and testing)",
			EnvVar: twEnvVars("BUILD_API_INSECURE_SKIP_VERIFY"),
		},
		cli.StringFlag{Name: "pprof-port", Usage: "enable pprof http endpoint at port", EnvVar: twEnvVars("PPROF_PORT")},
		cli.BoolFlag{Name: "echo-config", Usage: "echo parsed config and exit", EnvVar: twEnvVars("ECHO_CONFIG")},
		cli.BoolFlag{Name: "debug", Usage: "set log level to debug", EnvVar: twEnvVars("DEBUG")},
	}
)

func twEnvVars(key string) string {
	return strings.ToUpper(strings.Join(twEnvVarsSlice(key), ","))
}

func twEnvVarsSlice(key string) []string {
	return []string{
		fmt.Sprintf("TRAVIS_WORKER_%s", key),
		key,
	}
}
