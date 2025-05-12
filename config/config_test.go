package config

import (
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gopkg.in/urfave/cli.v1"
)

func runAppTest(t *testing.T, args []string, action func(*cli.Context) error) {
	app := cli.NewApp()
	app.Flags = Flags
	app.Action = action
	_ = app.Run(append([]string{"whatever"}, args...))
}

func TestFromCLIContext(t *testing.T) {
	runAppTest(t, []string{}, func(c *cli.Context) error {
		cfg := FromCLIContext(c)

		assert.NotNil(t, cfg)
		return nil
	})
}

func TestFromCLIContext_SetsBoolFlags(t *testing.T) {
	runAppTest(t, []string{
		"--build-api-insecure-skip-verify",
		"--build-fix-etc-hosts",
		"--build-fix-resolv-conf",
		"--build-paranoid",
		"--sentry-hook-errors",
		"--skip-shutdown-on-log-timeout",
	}, func(c *cli.Context) error {
		cfg := FromCLIContext(c)

		assert.True(t, cfg.BuildAPIInsecureSkipVerify, "BuildAPIInsecureSkipVerify")
		assert.True(t, cfg.BuildFixEtcHosts, "BuildFixEtcHosts")
		assert.True(t, cfg.BuildFixResolvConf, "BuildFixResolvConf")
		assert.True(t, cfg.BuildParanoid, "BuildParanoid")
		assert.True(t, cfg.SentryHookErrors, "SentryHookErrors")
		assert.True(t, cfg.SkipShutdownOnLogTimeout, "SkipShutdownOnLogTimeout")

		return nil
	})
}

func TestFromCLIContext_SetsStringFlags(t *testing.T) {
	runAppTest(t, []string{
		"--amqp-uri=amqp://",
		"--amqp-tls-cert=cert",
		"--base-dir=dir",
		"--build-api-uri=http://build/api",
		"--build-apt-cache=cache",
		"--build-cache-s3-access-key-id=id",
		"--build-cache-s3-bucket=bucket",
		"--build-cache-s3-region=region",
		"--build-cache-s3-scheme=scheme",
		"--build-cache-s3-secret-access-key=key",
		"--build-cache-type=type",
		"--build-npm-cache=cache",
		"--default-dist=dist",
		"--default-arch=arch",
		"--default-group=group",
		"--default-language=language",
		"--default-os=os",
		"--hostname=hostname",
		"--librato-email=email",
		"--librato-source=source",
		"--librato-token=token",
		"--logs-amqp-uri=amqp://logs",
		"--provider-name=provider",
		"--queue-name=name",
		"--queue-type=type",
		"--sentry-dsn=dsn",
		"--artifact-manager-api-uri=http://artifact-manager/api",
	}, func(c *cli.Context) error {
		cfg := FromCLIContext(c)

		assert.Equal(t, "amqp://", cfg.AmqpURI, "AmqpURI")
		assert.Equal(t, "cert", cfg.AmqpTlsCert, "AmqpTlsCert")
		assert.Equal(t, "dir", cfg.BaseDir, "BaseDir")
		assert.Equal(t, "http://build/api", cfg.BuildAPIURI, "BuildAPIURI")
		assert.Equal(t, "cache", cfg.BuildAptCache, "BuildAptCache")
		assert.Equal(t, "id", cfg.BuildCacheS3AccessKeyID, "BuildCacheS3AccessKeyID")
		assert.Equal(t, "bucket", cfg.BuildCacheS3Bucket, "BuildCacheS3Bucket")
		assert.Equal(t, "scheme", cfg.BuildCacheS3Scheme, "BuildCacheS3Scheme")
		assert.Equal(t, "key", cfg.BuildCacheS3SecretAccessKey, "BuildCacheS3SecretAccessKey")
		assert.Equal(t, "type", cfg.BuildCacheType, "BuildCacheType")
		assert.Equal(t, "cache", cfg.BuildNpmCache, "BuildNpmCache")
		assert.Equal(t, "dist", cfg.DefaultDist, "DefaultDist")
		assert.Equal(t, "arch", cfg.DefaultArch, "DefaultArch")
		assert.Equal(t, "group", cfg.DefaultGroup, "DefaultGroup")
		assert.Equal(t, "language", cfg.DefaultLanguage, "DefaultLanguage")
		assert.Equal(t, "os", cfg.DefaultOS, "DefaultOS")
		assert.Equal(t, "hostname", cfg.Hostname, "Hostname")
		assert.Equal(t, "email", cfg.LibratoEmail, "LibratoEmail")
		assert.Equal(t, "source", cfg.LibratoSource, "LibratoSource")
		assert.Equal(t, "token", cfg.LibratoToken, "LibratoToken")
		assert.Equal(t, "amqp://logs", cfg.LogsAmqpURI, "LogsAmqpURI")
		assert.Equal(t, "provider", cfg.ProviderName, "ProviderName")
		assert.Equal(t, "name", cfg.QueueName, "QueueName")
		assert.Equal(t, "type", cfg.QueueType, "QueueType")
		assert.Equal(t, "dsn", cfg.SentryDSN, "SentryDSN")

		return nil
	})
}

func TestFromCLIContext_SetsIntFlags(t *testing.T) {
	runAppTest(t, []string{
		"--pool-size=42",
	}, func(c *cli.Context) error {
		cfg := FromCLIContext(c)

		assert.Equal(t, 42, cfg.PoolSize, "PoolSize")

		return nil
	})
}

func TestFromCLIContext_SetsDurationFlags(t *testing.T) {
	runAppTest(t, []string{
		"--file-polling-interval=42s",
		"--hard-timeout=2h",
		"--log-timeout=11m",
		"--script-upload-timeout=2m",
		"--startup-timeout=3m",
		"--build-cache-fetch-timeout=7m",
		"--build-cache-push-timeout=8m",
	}, func(c *cli.Context) error {
		cfg := FromCLIContext(c)

		assert.Equal(t, 42*time.Second, cfg.FilePollingInterval, "FilePollingInterval")
		assert.Equal(t, 2*time.Hour, cfg.HardTimeout, "HardTimeout")
		assert.Equal(t, 11*time.Minute, cfg.LogTimeout, "LogTimeout")
		assert.Equal(t, 2*time.Minute, cfg.ScriptUploadTimeout, "ScriptUploadTimeout")
		assert.Equal(t, 3*time.Minute, cfg.StartupTimeout, "StartupTimeout")
		assert.Equal(t, 7*time.Minute, cfg.BuildCacheFetchTimeout, "BuildCacheFetchTimeout")
		assert.Equal(t, 8*time.Minute, cfg.BuildCachePushTimeout, "BuildCachePushTimeout")

		return nil
	})
}

func TestFromCLIContext_SetsProviderConfig(t *testing.T) {
	i := fmt.Sprintf("%v", rand.Int())
	os.Setenv("TRAVIS_WORKER_FAKE_FOO", i)

	runAppTest(t, []string{
		"--provider-name=fake",
	}, func(c *cli.Context) error {
		cfg := FromCLIContext(c)

		assert.NotNil(t, cfg.ProviderConfig)
		assert.Equal(t, i, cfg.ProviderConfig.Get("FOO"))

		return nil
	})
}
