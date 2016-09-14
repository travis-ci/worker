// Package config contains the flags and defaults for Worker configuration.
package config

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"gopkg.in/urfave/cli.v1"
)

var (
	defaultAmqpURI                = "amqp://"
	defaultBaseDir                = "."
	defaultFilePollingInterval, _ = time.ParseDuration("5s")
	defaultPoolSize               = 1
	defaultProviderName           = "docker"
	defaultQueueType              = "amqp"

	defaultHardTimeout, _         = time.ParseDuration("50m")
	defaultInitialSleep, _        = time.ParseDuration("1s")
	defaultLogTimeout, _          = time.ParseDuration("10m")
	defaultMaxLogLength           = 4500000
	defaultScriptUploadTimeout, _ = time.ParseDuration("3m30s")
	defaultStartupTimeout, _      = time.ParseDuration("4m")

	defaultBuildCacheFetchTimeout, _ = time.ParseDuration("5m")
	defaultBuildCachePushTimeout, _  = time.ParseDuration("5m")

	defaultHostname, _ = os.Hostname()
	defaultLanguage    = "default"
	defaultDist        = "precise"
	defaultGroup       = "stable"
	defaultOS          = "linux"

	configType = reflect.ValueOf(Config{}).Type()

	defs = []*ConfigDef{
		NewConfigDef("ProviderName", &cli.StringFlag{
			Value: defaultProviderName,
			Usage: "The name of the provider to use. See below for provider-specific configuration",
		}),
		NewConfigDef("QueueType", &cli.StringFlag{
			Value: defaultQueueType,
			Usage: `The name of the queue type to use ("amqp", "http", or "file")`,
		}),
		NewConfigDef("AmqpURI", &cli.StringFlag{
			Value: defaultAmqpURI,
			Usage: `The URI to the AMQP server to connect to (only valid for "amqp" queue type)`,
		}),
		NewConfigDef("AmqpInsecure", &cli.BoolFlag{
			Usage: `Whether to connect to the AMQP server without verifying TLS certificates (only valid for "amqp" queue type)`,
		}),
		NewConfigDef("AmqpTlsCert", &cli.StringFlag{
			Usage: `The TLS certificate used to connet to the AMQP server`,
		}),
		NewConfigDef("AmqpTlsCertPath", &cli.StringFlag{
			Usage: `Path to the TLS certificate used to connet to the AMQP server`,
		}),
		NewConfigDef("BaseDir", &cli.StringFlag{
			Value: defaultBaseDir,
			Usage: `The base directory for file-based queues (only valid for "file" queue type)`,
		}),
		NewConfigDef("FilePollingInterval", &cli.DurationFlag{
			Value: defaultFilePollingInterval,
			Usage: `The interval at which file-based queues are checked (only valid for "file" queue type)`,
		}),
		NewConfigDef("PoolSize", &cli.IntFlag{
			Value: defaultPoolSize,
			Usage: "The size of the processor pool, affecting the number of jobs this worker can run in parallel",
		}),
		NewConfigDef("BuildAPIURI", &cli.StringFlag{
			Usage: "The full URL to the build API endpoint to use. Note that this also requires the path of the URL. If a username is included in the URL, this will be translated to a token passed in the Authorization header",
		}),
		NewConfigDef("QueueName", &cli.StringFlag{
			Usage: "The AMQP queue to subscribe to for jobs",
		}),
		NewConfigDef("LibratoEmail", &cli.StringFlag{
			Usage: "Librato metrics account email",
		}),
		NewConfigDef("LibratoToken", &cli.StringFlag{
			Usage: "Librato metrics account token",
		}),
		NewConfigDef("LibratoSource", &cli.StringFlag{
			Value: defaultHostname,
			Usage: "Librato metrics source name",
		}),
		NewConfigDef("SentryDSN", &cli.StringFlag{
			Usage: "The DSN to send Sentry events to",
		}),
		NewConfigDef("SentryHookErrors", &cli.BoolFlag{
			Usage: "Add logrus.ErrorLevel to logrus sentry hook",
		}),
		NewConfigDef("Hostname", &cli.StringFlag{
			Value: defaultHostname,
			Usage: "Host name used in log output to identify the source of a job",
		}),
		NewConfigDef("DefaultLanguage", &cli.StringFlag{
			Value: defaultLanguage,
			Usage: "Default \"language\" value for each job",
		}),
		NewConfigDef("DefaultDist", &cli.StringFlag{
			Value: defaultDist,
			Usage: "Default \"dist\" value for each job",
		}),
		NewConfigDef("DefaultGroup", &cli.StringFlag{
			Value: defaultGroup,
			Usage: "Default \"group\" value for each job",
		}),
		NewConfigDef("DefaultOS", &cli.StringFlag{
			Value: defaultOS,
			Usage: "Default \"os\" value for each job",
		}),
		NewConfigDef("HardTimeout", &cli.DurationFlag{
			Value: defaultHardTimeout,
			Usage: "The outermost (maximum) timeout for a given job, at which time the job is cancelled",
		}),
		NewConfigDef("InitialSleep", &cli.DurationFlag{
			Value: defaultInitialSleep,
			Usage: "The time to sleep prior to opening log and starting job",
		}),
		NewConfigDef("LogTimeout", &cli.DurationFlag{
			Value: defaultLogTimeout,
			Usage: "The timeout for a job that's not outputting anything",
		}),
		NewConfigDef("ScriptUploadTimeout", &cli.DurationFlag{
			Value: defaultScriptUploadTimeout,
			Usage: "The timeout for the script upload step",
		}),
		NewConfigDef("StartupTimeout", &cli.DurationFlag{
			Value: defaultStartupTimeout,
			Usage: "The timeout for execution environment to be ready",
		}),
		NewConfigDef("MaxLogLength", &cli.IntFlag{
			Value: defaultMaxLogLength,
			Usage: "The maximum length of a log in bytes",
		}),
		NewConfigDef("JobBoardURL", &cli.StringFlag{
			Usage: "The base URL for job-board used with http queue",
		}),
		NewConfigDef("TravisSite", &cli.StringFlag{
			Usage: "Either 'org' or 'com', used for job-board",
		}),

		// build script generator flags
		NewConfigDef("BuildCacheFetchTimeout", &cli.DurationFlag{
			Value: defaultBuildCacheFetchTimeout,
		}),
		NewConfigDef("BuildCachePushTimeout", &cli.DurationFlag{
			Value: defaultBuildCachePushTimeout,
		}),
		NewConfigDef("BuildAptCache", &cli.StringFlag{}),
		NewConfigDef("BuildNpmCache", &cli.StringFlag{}),
		NewConfigDef("BuildParanoid", &cli.BoolFlag{}),
		NewConfigDef("BuildFixResolvConf", &cli.BoolFlag{}),
		NewConfigDef("BuildFixEtcHosts", &cli.BoolFlag{}),
		NewConfigDef("BuildCacheType", &cli.StringFlag{}),
		NewConfigDef("BuildCacheS3Scheme", &cli.StringFlag{}),
		NewConfigDef("BuildCacheS3Region", &cli.StringFlag{}),
		NewConfigDef("BuildCacheS3Bucket", &cli.StringFlag{}),
		NewConfigDef("BuildCacheS3AccessKeyID", &cli.StringFlag{}),
		NewConfigDef("BuildCacheS3SecretAccessKey", &cli.StringFlag{}),

		// non-config and special case flags
		NewConfigDef("FilterJobJsonScript", &cli.StringFlag{
			Usage: "External script which will be called to filter the json to be sent to the build script generator",
		}),
		NewConfigDef("SkipShutdownOnLogTimeout", &cli.BoolFlag{
			Usage: "Special-case mode to aid with debugging timed out jobs",
		}),
		NewConfigDef("BuildAPIInsecureSkipVerify", &cli.BoolFlag{
			Usage: "Skip build API TLS verification (useful for Enterprise and testing)",
		}),
		NewConfigDef("pprof-port", &cli.StringFlag{
			Usage: "enable pprof http endpoint (and internal http api) at port",
		}),
		NewConfigDef("http-api-port", &cli.StringFlag{
			Usage: "enable http api (and pprof) at port",
		}),
		NewConfigDef("http-api-auth", &cli.StringFlag{
			Usage: "username:password for http api basic auth",
		}),
		NewConfigDef("silence-metrics", &cli.BoolFlag{
			Usage: "silence metrics logging in case no Librato creds have been provided",
		}),
		NewConfigDef("echo-config", &cli.BoolFlag{
			Usage: "echo parsed config and exit",
		}),
		NewConfigDef("list-backend-providers", &cli.BoolFlag{
			Usage: "echo backend provider list and exit",
		}),
		NewConfigDef("debug", &cli.BoolFlag{
			Usage: "set log level to debug",
		}),
		NewConfigDef("start-hook", &cli.StringFlag{
			Usage: "executable to run just before starting",
		}),
		NewConfigDef("stop-hook", &cli.StringFlag{
			Usage: "executable to run just before exiting",
		}),
		NewConfigDef("heartbeat-url", &cli.StringFlag{
			Usage: "health check and/or supervisor check URL (expects response: {\"state\": \"(up|down)\"})",
		}),
		NewConfigDef("heartbeat-url-auth-token", &cli.StringFlag{
			Usage: "auth token for health check and/or supervisor check URL (may be \"file://path/to/file\")",
		}),
	}

	// Flags is the list of all CLI flags accepted by travis-worker
	Flags = defFlags(defs)
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

func init() {
	wd, err := os.Getwd()
	if err != nil {
		return
	}

	defaultBaseDir = wd
}

func defFlags(defs []*ConfigDef) []cli.Flag {
	f := []cli.Flag{}

	for _, def := range defs {
		f = append(f, def.Flag)
	}

	return f
}

type ConfigDef struct {
	FieldName string
	Name      string
	EnvVar    string
	Flag      cli.Flag
	HasField  bool
}

func NewConfigDef(fieldName string, flag cli.Flag) *ConfigDef {
	if fieldName == "" {
		panic("empty field name")
	}

	var name string
	if string(fieldName[0]) == strings.ToLower(string(fieldName[0])) {
		name = fieldName
	} else {
		field, _ := configType.FieldByName(fieldName)
		name = field.Tag.Get("config")
	}

	env := strings.ToUpper(strings.Replace(name, "-", "_", -1))

	def := &ConfigDef{
		FieldName: fieldName,
		Name:      name,
		EnvVar:    env,
		HasField:  fieldName != name,
	}

	envPrefixed := twEnvVars(env)

	if f, ok := flag.(*cli.BoolFlag); ok {
		def.Flag, f.Name, f.EnvVar = f, name, envPrefixed
		return def
	} else if f, ok := flag.(*cli.StringFlag); ok {
		def.Flag, f.Name, f.EnvVar = f, name, envPrefixed
		return def
	} else if f, ok := flag.(*cli.IntFlag); ok {
		def.Flag, f.Name, f.EnvVar = f, name, envPrefixed
		return def
	} else if f, ok := flag.(*cli.DurationFlag); ok {
		def.Flag, f.Name, f.EnvVar = f, name, envPrefixed
		return def
	} else {
		return def
	}
}

// Config contains all the configuration needed to run the worker.
type Config struct {
	ProviderName    string `config:"provider-name"`
	QueueType       string `config:"queue-type"`
	AmqpURI         string `config:"amqp-uri"`
	AmqpInsecure    bool   `config:"amqp-insecure"`
	AmqpTlsCert     string `config:"amqp-tls-cert"`
	AmqpTlsCertPath string `config:"amqp-tls-cert-path"`
	BaseDir         string `config:"base-dir"`
	PoolSize        int    `config:"pool-size"`
	BuildAPIURI     string `config:"build-api-uri"`
	QueueName       string `config:"queue-name"`
	LibratoEmail    string `config:"librato-email"`
	LibratoToken    string `config:"librato-token"`
	LibratoSource   string `config:"librato-source"`
	SentryDSN       string `config:"sentry-dsn"`
	Hostname        string `config:"hostname"`
	DefaultLanguage string `config:"default-language"`
	DefaultDist     string `config:"default-dist"`
	DefaultGroup    string `config:"default-group"`
	DefaultOS       string `config:"default-os"`
	JobBoardURL     string `config:"job-board-url"`
	TravisSite      string `config:"travis-site"`

	FilePollingInterval time.Duration `config:"file-polling-interval"`

	HardTimeout         time.Duration `config:"hard-timeout"`
	InitialSleep        time.Duration `config:"initial-sleep"`
	LogTimeout          time.Duration `config:"log-timeout"`
	MaxLogLength        int           `config:"max-log-length"`
	ScriptUploadTimeout time.Duration `config:"script-upload-timeout"`
	StartupTimeout      time.Duration `config:"startup-timeout"`

	SentryHookErrors           bool `config:"sentry-hook-errors"`
	BuildAPIInsecureSkipVerify bool `config:"build-api-insecure-skip-verify"`
	SkipShutdownOnLogTimeout   bool `config:"skip-shutdown-on-log-timeout"`

	// build script generator options
	BuildCacheFetchTimeout time.Duration `config:"build-cache-fetch-timeout"`
	BuildCachePushTimeout  time.Duration `config:"build-cache-push-timeout"`

	BuildParanoid      bool `config:"build-paranoid"`
	BuildFixResolvConf bool `config:"build-fix-resolv-conf"`
	BuildFixEtcHosts   bool `config:"build-fix-etc-hosts"`

	BuildAptCache               string `config:"build-apt-cache"`
	BuildNpmCache               string `config:"build-npm-cache"`
	BuildCacheType              string `config:"build-cache-type"`
	BuildCacheS3Scheme          string `config:"build-cache-s3-scheme"`
	BuildCacheS3Region          string `config:"build-cache-s3-region"`
	BuildCacheS3Bucket          string `config:"build-cache-s3-bucket"`
	BuildCacheS3AccessKeyID     string `config:"build-cache-s3-access-key-id"`
	BuildCacheS3SecretAccessKey string `config:"build-cache-s3-secret-access-key"`

	FilterJobJsonScript string `config:"filter-job-json-script"`

	ProviderConfig *ProviderConfig
}

// FromCLIContext creates a Config using a cli.Context by pulling configuration
// from the flags in the context.
func FromCLIContext(c *cli.Context) *Config {
	cfg := &Config{}
	cfgVal := reflect.ValueOf(cfg).Elem()

	for _, def := range defs {
		if !def.HasField {
			continue
		}

		field := cfgVal.FieldByName(def.FieldName)

		if _, ok := def.Flag.(*cli.BoolFlag); ok {
			field.SetBool(c.Bool(def.Name))
		} else if _, ok := def.Flag.(*cli.DurationFlag); ok {
			field.Set(reflect.ValueOf(c.Duration(def.Name)))
		} else if _, ok := def.Flag.(*cli.IntFlag); ok {
			field.SetInt(int64(c.Int(def.Name)))
		} else if _, ok := def.Flag.(*cli.StringFlag); ok {
			field.SetString(c.String(def.Name))
		}
	}

	cfg.ProviderConfig = ProviderConfigFromEnviron(cfg.ProviderName)

	return cfg
}

// WriteEnvConfig writes the given configuration to out. The format of the
// output is a list of environment variables settings suitable to be sourced
// by a Bourne-like shell.
func WriteEnvConfig(cfg *Config, out io.Writer) {
	cfgMap := map[string]interface{}{}
	cfgElem := reflect.ValueOf(cfg).Elem()

	for _, def := range defs {
		if !def.HasField {
			continue
		}

		field := cfgElem.FieldByName(def.FieldName)
		cfgMap[def.Name] = field.Interface()
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
