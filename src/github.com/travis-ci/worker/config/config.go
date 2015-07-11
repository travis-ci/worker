package config

import (
	"fmt"
	"io"
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
	LogTimeout     time.Duration

	BuildAPIInsecureSkipVerify bool
	SkipShutdownOnLogTimeout   bool

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

// FromCLIContext creates a Config using a cli.Context by pulling configuration
// from the flags in the context.
func FromCLIContext(c *cli.Context) *Config {
	cfg := &Config{
		AmqpURI:       c.String("amqp-uri"),
		PoolSize:      c.Int("pool-size"),
		BuildAPIURI:   c.String("build-api-uri"),
		ProviderName:  c.String("provider-name"),
		QueueName:     c.String("queue-name"),
		LibratoEmail:  c.String("librato-email"),
		LibratoToken:  c.String("librato-token"),
		LibratoSource: c.String("librato-source"),
		SentryDSN:     c.String("sentry-dsn"),
		Hostname:      c.String("hostname"),
		HardTimeout:   c.Duration("hard-timeout"),
		LogTimeout:    c.Duration("log-timeout"),

		BuildAPIInsecureSkipVerify: c.Bool("build-api-insecure-skip-verify"),
		SkipShutdownOnLogTimeout:   c.Bool("skip-shutdown-on-log-timeout"),

		BuildCacheFetchTimeout:      c.Duration("build-cache-fetch-timeout"),
		BuildCachePushTimeout:       c.Duration("build-cache-push-timeout"),
		BuildAptCache:               c.String("build-apt-cache"),
		BuildNpmCache:               c.String("build-npm-cache"),
		BuildParanoid:               c.Bool("build-paranoid"),
		BuildFixResolvConf:          c.Bool("build-fix-resolv-conf"),
		BuildFixEtcHosts:            c.Bool("build-fix-etc-hosts"),
		BuildCacheType:              c.String("build-cache-type"),
		BuildCacheS3Scheme:          c.String("build-cache-s3-scheme"),
		BuildCacheS3Region:          c.String("build-cache-s3-region"),
		BuildCacheS3Bucket:          c.String("build-cache-s3-bucket"),
		BuildCacheS3AccessKeyID:     c.String("build-cache-s3-access-key-id"),
		BuildCacheS3SecretAccessKey: c.String("build-cache-s3-secret-access-key"),
	}

	cfg.ProviderConfig = ProviderConfigFromEnviron(cfg.ProviderName)

	return cfg
}

// WriteEnvConfig writes the given configuration to out. The format of the
// output is a list of environment variables settings suitable to be sourced
// by a Bourne-like shell.
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

		"build-api-insecure-skip-verify": cfg.BuildAPIInsecureSkipVerify,
		"skip-shutdown-on-log-timeout":   cfg.SkipShutdownOnLogTimeout,

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
