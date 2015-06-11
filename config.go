package worker

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/codegangsta/cli"
)

var (
	defaultAmqpURI                   = "amqp://"
	defaultPoolSize                  = 1
	defaultProviderName              = "docker"
	defaultHardTimeout, _            = time.ParseDuration("50m")
	defaultBuildCacheFetchTimeout, _ = time.ParseDuration("5m")
	defaultBuildCachePushTimeout, _  = time.ParseDuration("5m")

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
		cli.StringFlag{Name: "pprof-port", EnvVar: twEnvVars("PPROF_PORT")},
		cli.BoolFlag{Name: "debug", EnvVar: twEnvVars("DEBUG")},

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
	}
)

func twEnvVars(key string) string {
	return strings.ToUpper(fmt.Sprintf("TRAVIS_WORKER_%s,%s", key, key))
}

// Config contains all the configuration needed to run the worker.
type Config struct {
	AmqpURI       string
	PoolSize      int
	BuildAPIURI   string
	ProviderName  string
	QueueName     string
	LibratoEmail  string
	LibratoToken  string
	LibratoSource string
	SentryDSN     string
	Hostname      string
	HardTimeout   time.Duration

	SkipShutdownOnLogTimeout bool

	// build script generator options
	BuildCacheFetchTimeout      time.Duration
	BuildCachePushTimeout       time.Duration
	BuildAptCache               string
	BuildNpmCache               string
	BuildParanoid               bool
	BuildFixResolvConf          bool
	BuildFixEtcHosts            bool
	BuildCacheType              string
	BuildCacheS3Scheme          string
	BuildCacheS3Region          string
	BuildCacheS3Bucket          string
	BuildCacheS3AccessKeyID     string
	BuildCacheS3SecretAccessKey string
}

func cfgString(c *cli.Context, name, dflt string) string {
	if !c.IsSet(name) {
		return dflt
	}
	return c.String(name)
}

func cfgInt(c *cli.Context, name string, dflt int) int {
	if !c.IsSet(name) {
		return dflt
	}
	return c.Int(name)
}

func cfgDuration(c *cli.Context, name string, dflt time.Duration) time.Duration {
	if !c.IsSet(name) {
		return dflt
	}
	return c.Duration(name)
}

func cfgBool(c *cli.Context, name string, dflt bool) bool {
	if !c.IsSet(name) {
		return dflt
	}
	return c.Bool(name)
}

func ConfigFromCLIContext(c *cli.Context) *Config {
	hostname, _ := os.Hostname()

	return &Config{
		AmqpURI:       cfgString(c, "amqp-uri", defaultAmqpURI),
		PoolSize:      cfgInt(c, "pool-size", defaultPoolSize),
		BuildAPIURI:   cfgString(c, "build-api-uri", ""),
		ProviderName:  cfgString(c, "provider-name", defaultProviderName),
		QueueName:     cfgString(c, "queue-name", ""),
		LibratoEmail:  cfgString(c, "librato-email", ""),
		LibratoToken:  cfgString(c, "librato-token", ""),
		LibratoSource: cfgString(c, "librato-source", ""),
		SentryDSN:     cfgString(c, "sentry-dsn", ""),
		Hostname:      cfgString(c, "hostname", hostname),
		HardTimeout:   cfgDuration(c, "hard-timeout", defaultHardTimeout),

		SkipShutdownOnLogTimeout: cfgBool(c, "skip-shutdown-on-log-timeout", false),

		BuildCacheFetchTimeout:      cfgDuration(c, "build-cache-fetch-timeout", defaultBuildCacheFetchTimeout),
		BuildCachePushTimeout:       cfgDuration(c, "build-cache-push-timeout", defaultBuildCachePushTimeout),
		BuildAptCache:               cfgString(c, "build-apt-cache", ""),
		BuildNpmCache:               cfgString(c, "build-npm-cache", ""),
		BuildParanoid:               cfgBool(c, "build-paranoid", true),
		BuildFixResolvConf:          cfgBool(c, "build-fix-resolv-conf", false),
		BuildFixEtcHosts:            cfgBool(c, "build-fix-etc-hosts", false),
		BuildCacheType:              cfgString(c, "build-cache-type", ""),
		BuildCacheS3Scheme:          cfgString(c, "build-cache-s3-scheme", ""),
		BuildCacheS3Region:          cfgString(c, "build-cache-s3-region", ""),
		BuildCacheS3Bucket:          cfgString(c, "build-cache-s3-bucket", ""),
		BuildCacheS3AccessKeyID:     cfgString(c, "build-cache-s3-access-key-id", ""),
		BuildCacheS3SecretAccessKey: cfgString(c, "build-cache-s3-secret-access-key", ""),
	}
}
