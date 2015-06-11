package config

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/codegangsta/cli"
)

var (
	zeroDuration time.Duration
)

// Config contains all the configuration needed to run the worker.
type Config struct {
	AmqpURI        string
	PoolSize       int
	BuildAPIURI    string
	ProviderName   string
	ProviderConfig *ProviderConfig
	QueueName      string
	LibratoEmail   string
	LibratoToken   string
	LibratoSource  string
	SentryDSN      string
	Hostname       string
	HardTimeout    time.Duration

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
	for _, strval := range []string{c.String(name), c.GlobalString(name), dflt} {
		if strval != "" {
			return strval
		}
	}
	return dflt
}

func cfgInt(c *cli.Context, name string, dflt int) int {
	for _, intval := range []int{c.Int(name), c.GlobalInt(name), dflt} {
		if intval != 0 {
			return intval
		}
	}

	return dflt
}

func cfgDuration(c *cli.Context, name string, dflt time.Duration) time.Duration {
	for _, durval := range []time.Duration{c.Duration(name), c.GlobalDuration(name), dflt} {
		if durval != zeroDuration {
			return durval
		}
	}

	return dflt
}

func cfgBool(c *cli.Context, name string, dflt bool) bool {
	return c.GlobalBool(name) || c.Bool(name) || dflt
}

func ConfigFromCLIContext(c *cli.Context) *Config {
	hostname, _ := os.Hostname()

	cfg := &Config{
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

	cfg.ProviderConfig = ProviderConfigFromEnviron(cfg.ProviderName)

	return cfg
}

func WriteEnvConfig(cfg *Config, out io.Writer) {
	cfgMap := map[string]interface{}{
		"amqp-uri":       cfg.AmqpURI,
		"pool-size":      cfg.PoolSize,
		"build-api-uri":  cfg.BuildAPIURI,
		"provider-name":  cfg.ProviderName,
		"queue-name":     cfg.QueueName,
		"librato-email":  cfg.LibratoEmail,
		"librato-token":  cfg.LibratoToken,
		"librato-source": cfg.LibratoSource,
		"sentry-dsn":     cfg.SentryDSN,
		"hostname":       cfg.Hostname,
		"hard-timout":    cfg.HardTimeout,

		"skip-shutdown-on-log-timeout": cfg.SkipShutdownOnLogTimeout,

		"build-cache-fetch-timeout":        cfg.BuildCacheFetchTimeout,
		"build-cache-push-timeout":         cfg.BuildCachePushTimeout,
		"build-opt-cache":                  cfg.BuildAptCache,
		"build-npm-cache":                  cfg.BuildNpmCache,
		"build-paranoid":                   cfg.BuildParanoid,
		"build-fix-resolv-conf":            cfg.BuildFixResolvConf,
		"build-fix-etc-hosts":              cfg.BuildFixEtcHosts,
		"build-cache-type":                 cfg.BuildCacheType,
		"build-cache-s3-scheme":            cfg.BuildCacheS3Scheme,
		"build-cache-s3-region":            cfg.BuildCacheS3Region,
		"build-cache-s3-bucket":            cfg.BuildCacheS3Bucket,
		"build-cache-s3-access-key-id":     cfg.BuildCacheS3AccessKeyID,
		"build-cache-s3-secret-access-key": cfg.BuildCacheS3SecretAccessKey,
	}

	sortedCfgMapKeys := []string{}

	for key, _ := range cfgMap {
		sortedCfgMapKeys = append(sortedCfgMapKeys, key)
	}

	sort.Strings(sortedCfgMapKeys)

	fmt.Fprintf(out, "# travis-worker env config generated %s\n", time.Now().UTC())
	for _, key := range sortedCfgMapKeys {
		envKey := fmt.Sprintf("TRAVIS_WORKER_%s", strings.ToUpper(strings.Replace(key, "-", "_", -1)))
		fmt.Fprintf(out, "export %s=%q\n", envKey, fmt.Sprintf("%v", cfgMap[key]))
	}
	fmt.Fprintf(out, "\n# travis-worker provider config:\n")
	cfg.ProviderConfig.Map(func(key, value string) {
		envKey := strings.ToUpper(fmt.Sprintf("TRAVIS_WORKER_%s_%s", cfg.ProviderName, strings.Replace(key, "-", "_", -1)))
		fmt.Fprintf(out, "export %s=%q\n", envKey, value)
	})
	fmt.Fprintf(out, "# end travis-worker env config\n")
}
