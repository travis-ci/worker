package lib

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/bitly/go-simplejson"
	"github.com/travis-ci/worker/lib/metrics"
	"golang.org/x/net/context"
)

// A BuildScriptGeneratorError is sometimes used by the Generate method on a
// BuildScriptGenerator to return more metadata about an error.
type BuildScriptGeneratorError struct {
	error

	// true when this error can be recovered by retrying later
	Recover bool
}

// A BuildScriptGenerator generates a build script for a given job payload.
type BuildScriptGenerator interface {
	Generate(context.Context, *simplejson.Json) ([]byte, error)
}

type webBuildScriptGenerator struct {
	URL               string
	aptCacheHost      string
	npmCacheHost      string
	paranoid          bool
	fixResolvConf     bool
	fixEtcHosts       bool
	cacheType         string
	cacheFetchTimeout int
	cachePushTimeout  int
	s3CacheOptions    s3BuildCacheOptions
}

type s3BuildCacheOptions struct {
	scheme          string
	region          string
	bucket          string
	accessKeyId     string
	secretAccessKey string
}

// NewBuildScriptGenerator creates a generator backed by an HTTP API.
func NewBuildScriptGenerator(URL string) BuildScriptGenerator {
	cacheFetchTimeout, _ := strconv.Atoi(os.Getenv("TRAVIS_WORKER_BUILD_CACHE_FETCH_TIMEOUT"))
	cachePushTimeout, _ := strconv.Atoi(os.Getenv("TRAVIS_WORKER_BUILD_CACHE_PUSH_TIMEOUT"))

	return &webBuildScriptGenerator{
		URL:               URL,
		aptCacheHost:      os.Getenv("TRAVIS_WORKER_BUILD_APT_CACHE"),
		npmCacheHost:      os.Getenv("TRAVIS_WORKER_BUILD_NPM_CACHE"),
		paranoid:          os.Getenv("TRAVIS_WORKER_BUILD_PARANOID") == "true",
		fixResolvConf:     os.Getenv("TRAVIS_WORKER_BUILD_FIX_RESOLV_CONF") == "true",
		fixEtcHosts:       os.Getenv("TRAVIS_WORKER_BUILD_FIX_ETC_HOSTS") == "true",
		cacheType:         os.Getenv("TRAVIS_WORKER_BUILD_CACHE_TYPE"),
		cacheFetchTimeout: cacheFetchTimeout,
		cachePushTimeout:  cachePushTimeout,
		s3CacheOptions: s3BuildCacheOptions{
			scheme:          os.Getenv("TRAVIS_WORKER_BUILD_CACHE_S3_SCHEME"),
			region:          os.Getenv("TRAVIS_WORKER_BUILD_CACHE_S3_REGION"),
			bucket:          os.Getenv("TRAVIS_WORKER_BUILD_CACHE_S3_BUCKET"),
			accessKeyId:     os.Getenv("TRAVIS_WORKER_BUILD_CACHE_S3_ACCESS_KEY_ID"),
			secretAccessKey: os.Getenv("TRAVIS_WORKER_BUILD_CACHE_S3_SECRET_ACCESS_KEY"),
		},
	}
}

func (g *webBuildScriptGenerator) Generate(ctx context.Context, payload *simplejson.Json) ([]byte, error) {
	if g.aptCacheHost != "" {
		payload.SetPath([]string{"hosts", "apt_cache"}, g.aptCacheHost)
	}
	if g.npmCacheHost != "" {
		payload.SetPath([]string{"hosts", "npm_cache"}, g.npmCacheHost)
	}

	payload.Set("paranoid", g.paranoid)
	payload.Set("fix_resolv_conf", g.fixResolvConf)
	payload.Set("fix_etc_hosts", g.fixEtcHosts)
	if g.cacheType != "" {
		payload.SetPath([]string{"cache_options", "type"}, g.cacheType)
		payload.SetPath([]string{"cache_options", "fetch_timeout"}, g.cacheFetchTimeout)
		payload.SetPath([]string{"cache_options", "push_timeout"}, g.cachePushTimeout)
		payload.SetPath([]string{"cache_options", "s3", "scheme"}, g.s3CacheOptions.scheme)
		payload.SetPath([]string{"cache_options", "s3", "region"}, g.s3CacheOptions.region)
		payload.SetPath([]string{"cache_options", "s3", "bucket"}, g.s3CacheOptions.bucket)
		payload.SetPath([]string{"cache_options", "s3", "access_key_id"}, g.s3CacheOptions.accessKeyId)
		payload.SetPath([]string{"cache_options", "s3", "secret_access_key"}, g.s3CacheOptions.secretAccessKey)
	}

	b, err := payload.Encode()
	if err != nil {
		return nil, err
	}

	var token string
	u, err := url.Parse(g.URL)
	if err != nil {
		return nil, err
	}
	if u.User != nil {
		token = u.User.Username()
		u.User = nil
	}

	buf := bytes.NewBuffer(b)
	req, err := http.NewRequest("POST", u.String(), buf)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("worker-go v=%v rev=%v d=%v", VersionString, RevisionString, GeneratedString))
	req.Header.Set("Content-Type", "application/json")

	startRequest := time.Now()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	metrics.TimeSince("worker.job.script.api", startRequest)

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 500 {
		return nil, BuildScriptGeneratorError{error: fmt.Errorf("server error: %q", string(body)), Recover: true}
	} else if resp.StatusCode >= 400 {
		return nil, BuildScriptGeneratorError{error: fmt.Errorf("client error: %q", string(body)), Recover: false}
	}

	return body, nil
}
