package config

import (
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
)

type ProviderConfig struct {
	sync.Mutex

	cfgMap map[string]string
}

func (pc *ProviderConfig) Map(f func(string, string)) {
	keys := []string{}
	for key, _ := range pc.cfgMap {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	for _, key := range keys {
		f(key, pc.Get(key))
	}
}

func (pc *ProviderConfig) Get(key string) string {
	pc.Lock()
	defer pc.Unlock()

	if value, ok := pc.cfgMap[key]; ok {
		return value
	}

	return ""
}

func (pc *ProviderConfig) Set(key, value string) {
	pc.Lock()
	defer pc.Unlock()

	pc.cfgMap[key] = value
}

func (pc *ProviderConfig) IsSet(key string) bool {
	pc.Lock()
	defer pc.Unlock()

	_, ok := pc.cfgMap[key]
	return ok
}

// ProviderConfigFromEnviron dynamically builds a *ProviderConfig from the
// environment by loading values from keys with prefixes that match either the
// uppercase provider name + "_" or "TRAVIS_WORKER_" + uppercase provider name +
// "_", e.g., for provider "foo":
//   env: TRAVIS_WORKER_FOO_BAR=ham FOO_BAZ=bones
//   map equiv: {"bar": "ham", "baz": "bones"}
func ProviderConfigFromEnviron(providerName string) *ProviderConfig {
	upperProvider := strings.ToUpper(providerName)

	pc := &ProviderConfig{cfgMap: map[string]string{}}

	for _, prefix := range []string{
		upperProvider + "_",
		"TRAVIS_WORKER_" + upperProvider + "_",
	} {
		for _, e := range os.Environ() {
			if strings.HasPrefix(e, prefix) {
				pair := strings.SplitN(e, "=", 2)

				key := strings.ToLower(strings.TrimPrefix(pair[0], prefix))
				value := pair[1]
				unescapedValue, err := url.QueryUnescape(value)
				if err == nil {
					value = unescapedValue
				}

				pc.Set(key, value)
			}
		}
	}

	return pc
}
