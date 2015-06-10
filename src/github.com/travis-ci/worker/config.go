package worker

import (
	"fmt"
	"os"
	"reflect"
)

// Config contains all the configuration needed to run the worker.
type Config struct {
	AmqpURI            string `env:"AMQP_URI"`
	PoolSize           uint16 `env:"POOL_SIZE"`
	BuildAPIURI        string `env:"BUILD_API_URI"`
	ProviderName       string `env:"PROVIDER_NAME"`
	ProviderConfig     string `env:"PROVIDER_CONFIG"`
	QueueName          string `env:"QUEUE_NAME"`
	LibratoEmail       string `env:"LIBRATO_EMAIL"`
	LibratoToken       string `env:"LIBRATO_TOKEN"`
	LibratoSource      string `env:"LIBRATO_SOURCE"`
	SentryDSN          string `env:"SENTRY_DSN"`
	Hostname           string `env:"HOSTNAME"`
	HardTimeoutSeconds uint64 `env:"HARD_TIMEOUT_SECONDS"`

	SkipShutdownOnLogTimeout string `env:"SKIP_SHUTDOWN_ON_LOG_TIMEOUT"`
}

// EnvToConfig creates a Config instance from the current environment variables
// using the env struct field tags in Config.
func EnvToConfig() Config {
	conf := Config{}
	s := reflect.ValueOf(&conf).Elem()
	typeOfConf := s.Type()
	for i := 0; i < s.NumField(); i++ {
		if typeOfConf.Field(i).Tag.Get("env") == "" {
			continue
		}

		s.Field(i).Set(valueForField(typeOfConf.Field(i)))
	}

	return conf
}

func valueForField(field reflect.StructField) reflect.Value {
	stringValue := os.Getenv(field.Tag.Get("env"))

	switch field.Type.Kind() {
	case reflect.String:
		return reflect.ValueOf(stringValue)
	case reflect.Uint16:
		var intValue uint16
		fmt.Sscanf(stringValue, "%d", &intValue)
		return reflect.ValueOf(intValue)
	case reflect.Int64:
		var intValue int64
		fmt.Sscanf(stringValue, "%d", &intValue)
		return reflect.ValueOf(intValue)
	case reflect.Uint64:
		var intValue uint64
		fmt.Sscanf(stringValue, "%d", &intValue)
		return reflect.ValueOf(intValue)
	}

	return reflect.Value{}
}
