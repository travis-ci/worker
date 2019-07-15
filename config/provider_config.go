package config

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
)

// ProviderConfig is the part of a configuration specific to a provider.
type ProviderConfig struct {
	sync.Mutex

	cfgMap map[string]string
}

// GoString formats the ProviderConfig as valid Go syntax. This makes
// ProviderConfig implement fmt.GoStringer.
func (pc *ProviderConfig) GoString() string {
	return fmt.Sprintf("&ProviderConfig{cfgMap: %#v}", pc.cfgMap)
}

// Each loops over all configuration settings and calls the given function with
// the key and value. The settings are sorted so f i called with the keys in
// alphabetical order.
func (pc *ProviderConfig) Each(f func(string, string)) {
	keys := []string{}
	for key := range pc.cfgMap {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	for _, key := range keys {
		f(key, pc.Get(key))
	}
}

// Get the value of a setting with the given key. The empty string is returned
// if the setting could not be found.
func (pc *ProviderConfig) Get(key string) string {
	pc.Lock()
	defer pc.Unlock()

	if value, ok := pc.cfgMap[key]; ok {
		return value
	}

	return ""
}

// Set the value of a setting with the given key.
func (pc *ProviderConfig) Set(key, value string) {
	pc.Lock()
	defer pc.Unlock()

	pc.cfgMap[key] = value
}

// Unset removes the given key from the config map
func (pc *ProviderConfig) Unset(key string) {
	pc.Lock()
	defer pc.Unlock()

	delete(pc.cfgMap, key)
}

// IsSet returns true if a setting with the given key exists, or false if it
// does not.
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
//   map equiv: {"BAR": "ham", "BAZ": "bones"}
func ProviderConfigFromEnviron(providerName string) *ProviderConfig {
	upperProvider := strings.ToUpper(providerName)

	pc := &ProviderConfig{cfgMap: map[string]string{}}

	for _, prefix := range []string{
		"TRAVIS_WORKER_" + upperProvider + "_",
		upperProvider + "_",
	} {
		for _, e := range os.Environ() {
			if strings.HasPrefix(e, prefix) {
				pair := strings.SplitN(e, "=", 2)

				key := strings.ToUpper(strings.TrimPrefix(pair[0], prefix))
				value := pair[1]
				if !strings.HasSuffix(key, "ACCOUNT_JSON") {
					unescapedValue, err := url.QueryUnescape(value)
					if err == nil {
						value = unescapedValue
					}
				}

				pc.Set(key, value)
			}
		}
	}

	return pc
}

// ProviderConfigFromMap creates a provider configuration backed by the given
// map. Useful for testing a provider.
func ProviderConfigFromMap(cfgMap map[string]string) *ProviderConfig {
	return &ProviderConfig{cfgMap: cfgMap}
}
