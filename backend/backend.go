package backend

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/codegangsta/cli"
)

var (
	backendRegistry      = map[string]*Backend{}
	backendRegistryMutex sync.Mutex
)

func backendStringFlag(alias, flagName, flagValue, envVar, flagUsage string) *cli.StringFlag {
	return &cli.StringFlag{
		Name:   flagName,
		Value:  flagValue,
		Usage:  flagUsage,
		EnvVar: beEnv(alias, envVar),
	}
}

func beEnv(alias, envVar string) string {
	return strings.Join([]string{
		fmt.Sprintf("%s_%s", strings.ToUpper(alias), envVar),
		fmt.Sprintf("TRAVIS_WORKER_%s_%s", strings.ToUpper(alias), envVar),
	}, ",")
}

func sliceToMap(sl []string) map[string]string {
	m := map[string]string{}

	for _, s := range sl {
		parts := strings.Split(s, "=")
		if len(parts) < 2 {
			continue
		}

		m[parts[0]] = parts[1]
	}

	return m
}

type ConfigGetter interface {
	String(string) string
	StringSlice(string) []string
	Bool(string) bool
	Int(string) int
	Duration(string) time.Duration
}

// Backend wraps up an alias, backend provider help, and a factory func for a
// given backend provider wheee
type Backend struct {
	Alias             string
	HumanReadableName string
	Flags             []cli.Flag
	ProviderFunc      func(ConfigGetter) (Provider, error)
}

// Register adds a backend to the registry!
func Register(alias, humanReadableName string, flags []cli.Flag, providerFunc func(ConfigGetter) (Provider, error)) {
	backendRegistryMutex.Lock()
	defer backendRegistryMutex.Unlock()

	backendRegistry[alias] = &Backend{
		Alias:             alias,
		HumanReadableName: humanReadableName,
		Flags:             flags,
		ProviderFunc:      providerFunc,
	}
}

// NewBackendProvider looks up a backend by its alias and returns a provider via
// the factory func on the registered *Backend
func NewBackendProvider(alias string, c ConfigGetter) (Provider, error) {
	backendRegistryMutex.Lock()
	defer backendRegistryMutex.Unlock()

	b, ok := backendRegistry[alias]
	if !ok {
		return nil, fmt.Errorf("unknown backend provider: %s", alias)
	}

	return b.ProviderFunc(c)
}

// EachBackend calls a given function for each registered backend
func EachBackend(f func(*Backend)) {
	backendRegistryMutex.Lock()
	defer backendRegistryMutex.Unlock()

	backendAliases := []string{}
	for backendAlias := range backendRegistry {
		backendAliases = append(backendAliases, backendAlias)
	}

	sort.Strings(backendAliases)

	for _, backendAlias := range backendAliases {
		f(backendRegistry[backendAlias])
	}
}
