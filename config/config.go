package config

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/codegangsta/cli"
)

// Config contains all the configuration needed to run the worker.
type Config struct {
	ProviderName        string
	QueueType           string
	AmqpURI             string
	BaseDir             string
	FilePollingInterval time.Duration
	PoolSize            int
	BuildAPIURI         string
	ProviderConfig      *ProviderConfig
	QueueName           string
	LibratoEmail        string
	LibratoToken        string
	LibratoSource       string
	SentryDSN           string
	Hostname            string
	DefaultLanguage     string
	DefaultDist         string
	DefaultGroup        string
	DefaultOS           string

	HardTimeout         time.Duration
	LogTimeout          time.Duration
	ScriptUploadTimeout time.Duration
	StartupTimeout      time.Duration

	SentryHookErrors           bool
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
		ProviderName:        c.String("provider-name"),
		QueueType:           c.String("queue-type"),
		AmqpURI:             c.String("amqp-uri"),
		BaseDir:             c.String("base-dir"),
		FilePollingInterval: c.Duration("file-polling-interval"),
		PoolSize:            c.Int("pool-size"),
		BuildAPIURI:         c.String("build-api-uri"),
		QueueName:           c.String("queue-name"),
		LibratoEmail:        c.String("librato-email"),
		LibratoToken:        c.String("librato-token"),
		LibratoSource:       c.String("librato-source"),
		SentryDSN:           c.String("sentry-dsn"),
		Hostname:            c.String("hostname"),
		DefaultLanguage:     c.String("default-language"),
		DefaultDist:         c.String("default-dist"),
		DefaultGroup:        c.String("default-group"),
		DefaultOS:           c.String("default-os"),

		HardTimeout:         c.Duration("hard-timeout"),
		LogTimeout:          c.Duration("log-timeout"),
		ScriptUploadTimeout: c.Duration("script-upload-timeout"),
		StartupTimeout:      c.Duration("startup-timeout"),

		SentryHookErrors:           c.Bool("sentry-hook-errors"),
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
		"provider-name":         cfg.ProviderName,
		"queue-type":            cfg.QueueType,
		"amqp-uri":              cfg.AmqpURI,
		"base-dir":              cfg.BaseDir,
		"file-polling-interval": cfg.FilePollingInterval,
		"pool-size":             cfg.PoolSize,
		"build-api-uri":         cfg.BuildAPIURI,
		"queue-name":            cfg.QueueName,
		"librato-email":         cfg.LibratoEmail,
		"librato-token":         cfg.LibratoToken,
		"librato-source":        cfg.LibratoSource,
		"sentry-dsn":            cfg.SentryDSN,
		"hostname":              cfg.Hostname,
		"default-language":      cfg.DefaultLanguage,
		"default-dist":          cfg.DefaultDist,
		"default-group":         cfg.DefaultGroup,
		"default-os":            cfg.DefaultOS,

		"hard-timeout":          cfg.HardTimeout,
		"log-timeout":           cfg.LogTimeout,
		"script-upload-timeout": cfg.ScriptUploadTimeout,
		"startup-timeout":       cfg.StartupTimeout,

		"sentry-hook-errors":             cfg.SentryHookErrors,
		"build-api-insecure-skip-verify": cfg.BuildAPIInsecureSkipVerify,
		"skip-shutdown-on-log-timeout":   cfg.SkipShutdownOnLogTimeout,

		"build-cache-fetch-timeout":        cfg.BuildCacheFetchTimeout,
		"build-cache-push-timeout":         cfg.BuildCachePushTimeout,
		"build-apt-cache":                  cfg.BuildAptCache,
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

	for key := range cfgMap {
		sortedCfgMapKeys = append(sortedCfgMapKeys, key)
	}

	sort.Strings(sortedCfgMapKeys)

	fmt.Fprintf(out, "# travis-worker env config generated %s\n", time.Now().UTC())
	for _, key := range sortedCfgMapKeys {
		envKey := fmt.Sprintf("TRAVIS_WORKER_%s", strings.ToUpper(strings.Replace(key, "-", "_", -1)))
		fmt.Fprintf(out, "export %s=%q\n", envKey, fmt.Sprintf("%v", cfgMap[key]))
	}
	fmt.Fprintf(out, "\n# travis-worker provider config:\n")
	cfg.ProviderConfig.Each(func(key, value string) {
		envKey := strings.ToUpper(fmt.Sprintf("TRAVIS_WORKER_%s_%s", cfg.ProviderName, strings.Replace(key, "-", "_", -1)))
		fmt.Fprintf(out, "export %s=%q\n", envKey, value)
	})
	fmt.Fprintf(out, "# end travis-worker env config\n")
}
