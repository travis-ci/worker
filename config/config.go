package config

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/codegangsta/cli"
)

var (
	DefaultConfig = &Config{
		BaseDir:         ".",
		AmqpURI:         "amqp://",
		PoolSize:        1,
		DefaultLanguage: "default",
		DefaultDist:     "precise",
		DefaultGroup:    "stable",
		DefaultOS:       "linux",
	}

	QueueTypes = []string{"amqp", "file"}

	configType = reflect.ValueOf(Config{}).Type()

	boolFlagType     = reflect.ValueOf(cli.BoolFlag{}).Type()
	durationFlagType = reflect.ValueOf(cli.DurationFlag{}).Type()
	intFlagType      = reflect.ValueOf(cli.IntFlag{}).Type()
	stringFlagType   = reflect.ValueOf(cli.StringFlag{}).Type()

	zeroStringValue = reflect.Zero(reflect.ValueOf("").Type())

	defs = []*ConfigDef{
		NewConfigDef("AmqpURI", []string{"amqp"}, &cli.StringFlag{
			Value: DefaultConfig.AmqpURI,
			Usage: `The URI to the AMQP server to connect to`,
		}),
		NewConfigDef("BaseDir", []string{"file"}, &cli.StringFlag{
			Value: DefaultConfig.BaseDir,
			Usage: `The base directory for file-based queues`,
		}),
		NewConfigDef("FilePollingInterval", []string{"file"}, &cli.DurationFlag{
			Value: DefaultConfig.FilePollingInterval,
			Usage: `The interval at which file-based queues are checked`,
		}),
		NewConfigDef("PoolSize", nil, &cli.IntFlag{
			Value: DefaultConfig.PoolSize,
			Usage: "The size of the processor pool, affecting the number of jobs this worker can run in parallel",
		}),
		NewConfigDef("BuildAPIURI", nil, &cli.StringFlag{
			Usage: "The full URL to the build API endpoint to use. Note that this also requires the path of the URL. If a username is included in the URL, this will be translated to a token passed in the Authorization header",
		}),
		NewConfigDef("QueueName", nil, &cli.StringFlag{
			Usage: "The queue to subscribe to for jobs",
		}),
		NewConfigDef("LibratoEmail", nil, &cli.StringFlag{
			Usage: "Librato metrics account email",
		}),
		NewConfigDef("LibratoToken", nil, &cli.StringFlag{
			Usage: "Librato metrics account token",
		}),
		NewConfigDef("LibratoSource", nil, &cli.StringFlag{
			Value: DefaultConfig.Hostname,
			Usage: "Librato metrics source name",
		}),
		NewConfigDef("SentryDSN", nil, &cli.StringFlag{
			Usage: "The DSN to send Sentry events to",
		}),
		NewConfigDef("SentryHookErrors", nil, &cli.BoolFlag{
			Usage: "Add logrus.ErrorLevel to logrus sentry hook",
		}),
		NewConfigDef("Hostname", nil, &cli.StringFlag{
			Value: DefaultConfig.Hostname,
			Usage: "Host name used in log output to identify the source of a job",
		}),
		NewConfigDef("DefaultLanguage", nil, &cli.StringFlag{
			Value: DefaultConfig.DefaultLanguage,
			Usage: "Default \"language\" value for each job",
		}),
		NewConfigDef("DefaultDist", nil, &cli.StringFlag{
			Value: DefaultConfig.DefaultDist,
			Usage: "Default \"dist\" value for each job",
		}),
		NewConfigDef("DefaultGroup", nil, &cli.StringFlag{
			Value: DefaultConfig.DefaultGroup,
			Usage: "Default \"group\" value for each job",
		}),
		NewConfigDef("DefaultOS", nil, &cli.StringFlag{
			Value: DefaultConfig.DefaultOS,
			Usage: "Default \"os\" value for each job",
		}),
		NewConfigDef("HardTimeout", nil, &cli.DurationFlag{
			Value: DefaultConfig.HardTimeout,
			Usage: "The outermost (maximum) timeout for a given job, at which time the job is cancelled",
		}),
		NewConfigDef("LogTimeout", nil, &cli.DurationFlag{
			Value: DefaultConfig.LogTimeout,
			Usage: "The timeout for a job that's not outputting anything",
		}),
		NewConfigDef("ScriptUploadTimeout", nil, &cli.DurationFlag{
			Value: DefaultConfig.ScriptUploadTimeout,
			Usage: "The timeout for the script upload step",
		}),
		NewConfigDef("StartupTimeout", nil, &cli.DurationFlag{
			Value: DefaultConfig.StartupTimeout,
			Usage: "The timeout for execution environment to be ready",
		}),

		// build script generator flags
		NewConfigDef("BuildCacheFetchTimeout", nil, &cli.DurationFlag{
			Value: DefaultConfig.BuildCacheFetchTimeout,
		}),
		NewConfigDef("BuildCachePushTimeout", nil, &cli.DurationFlag{
			Value: DefaultConfig.BuildCachePushTimeout,
		}),
		NewConfigDef("BuildAptCache", nil, &cli.StringFlag{}),
		NewConfigDef("BuildNpmCache", nil, &cli.StringFlag{}),
		NewConfigDef("BuildParanoid", nil, &cli.BoolFlag{}),
		NewConfigDef("BuildFixResolvConf", nil, &cli.BoolFlag{}),
		NewConfigDef("BuildFixEtcHosts", nil, &cli.BoolFlag{}),
		NewConfigDef("BuildCacheType", nil, &cli.StringFlag{}),
		NewConfigDef("BuildCacheS3Scheme", nil, &cli.StringFlag{}),
		NewConfigDef("BuildCacheS3Region", nil, &cli.StringFlag{}),
		NewConfigDef("BuildCacheS3Bucket", nil, &cli.StringFlag{}),
		NewConfigDef("BuildCacheS3AccessKeyID", nil, &cli.StringFlag{}),
		NewConfigDef("BuildCacheS3SecretAccessKey", nil, &cli.StringFlag{}),

		// non-config and special case flags
		NewConfigDef("SkipShutdownOnLogTimeout", nil, &cli.BoolFlag{
			Usage: "Special-case mode to aid with debugging timed out jobs",
		}),
		NewConfigDef("BuildAPIInsecureSkipVerify", nil, &cli.BoolFlag{
			Usage: "Skip build API TLS verification (useful for Enterprise and testing)",
		}),
		NewConfigDef("pprof-port", nil, &cli.StringFlag{
			Usage: "enable pprof http endpoint at port",
		}),
		NewConfigDef("SilenceMetrics", nil, &cli.BoolFlag{
			Usage: "silence metrics logging in case no Librato creds have been provided",
		}),
		NewConfigDef("Debug", nil, &cli.BoolFlag{
			Usage: "set log level to debug",
		}),
	}

	// WorkAMQPFlags is all CLI flags accepted by `travis-worker work amqp`
	WorkAMQPFlags = defFlags(defs, "amqp")

	// WorkFileFlags is all CLI flags accepted by `travis-worker work file`
	WorkFileFlags = defFlags(defs, "file")
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

	DefaultConfig.BaseDir = wd
	DefaultConfig.FilePollingInterval, _ = time.ParseDuration("5s")
	DefaultConfig.HardTimeout, _ = time.ParseDuration("50m")
	DefaultConfig.LogTimeout, _ = time.ParseDuration("10m")
	DefaultConfig.ScriptUploadTimeout, _ = time.ParseDuration("3m30s")
	DefaultConfig.StartupTimeout, _ = time.ParseDuration("4m")
	DefaultConfig.BuildCacheFetchTimeout, _ = time.ParseDuration("5m")
	DefaultConfig.BuildCachePushTimeout, _ = time.ParseDuration("5m")
	DefaultConfig.Hostname, _ = os.Hostname()
}

func defFlags(defs []*ConfigDef, flagSet string) []cli.Flag {
	f := []cli.Flag{}

	for _, def := range defs {
		if len(def.FlagSets) == 0 {
			f = append(f, def.Flag)
			continue
		}

		for _, fs := range def.FlagSets {
			if fs == flagSet {
				f = append(f, def.Flag)
			}
		}
	}

	return f
}

type ConfigDef struct {
	FieldName string
	FlagSets  []string
	Name      string
	EnvVar    string
	Flag      cli.Flag
	HasField  bool
}

func NewConfigDef(fieldName string, flagSets []string, flag cli.Flag) *ConfigDef {
	if fieldName == "" {
		panic("empty field name")
	}

	name := ""

	if string(fieldName[0]) == strings.ToLower(string(fieldName[0])) {
		name = fieldName
	} else {
		field, _ := configType.FieldByName(fieldName)
		name = field.Tag.Get("config")
	}

	env := strings.ToUpper(strings.Replace(name, "-", "_", -1))
	if flagSets == nil {
		flagSets = []string{}
	}

	def := &ConfigDef{
		FieldName: fieldName,
		FlagSets:  flagSets,
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

	FilePollingInterval time.Duration `config:"file-polling-interval"`
	HardTimeout         time.Duration `config:"hard-timeout"`
	LogTimeout          time.Duration `config:"log-timeout"`
	ScriptUploadTimeout time.Duration `config:"script-upload-timeout"`
	StartupTimeout      time.Duration `config:"startup-timeout"`

	SentryHookErrors           bool `config:"sentry-hook-errors"`
	BuildAPIInsecureSkipVerify bool `config:"build-api-insecure-skip-verify"`
	SkipShutdownOnLogTimeout   bool `config:"skip-shutdown-on-log-timeout"`
	Debug                      bool `config:"debug"`
	SilenceMetrics             bool `config:"silence-metrics"`

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
}

// FromCLIContext creates a Config using a cli.Context by pulling configuration
// from the flags in the context.
func FromCLIContext(c *cli.Context) *Config {
	cfg := DefaultConfig
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
	fmt.Fprintf(out, "# end travis-worker env config\n")
}
