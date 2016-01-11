package backend

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/codegangsta/cli"
)

var (
	backendRegistry      = map[string]*Backend{}
	backendRegistryMutex sync.Mutex
)

func backendStringFlag(alias, flagName, flagValue, flagUsage string, envVars []string) *cli.StringFlag {
	prefixedEnvVars := []string{}
	for _, e := range envVars {
		prefixedEnvVars = append(prefixedEnvVars,
			fmt.Sprintf("%s_%s", strings.ToUpper(alias), e),
			fmt.Sprintf("TRAVIS_WORKER_%s_%s", strings.ToUpper(alias), e))
	}

	return &cli.StringFlag{
		Name:   flagName,
		Value:  flagValue,
		Usage:  flagUsage,
		EnvVar: strings.Join(prefixedEnvVars, ","),
	}
}

// Backend wraps up an alias, backend provider help, and a factory func for a
// given backend provider wheee
type Backend struct {
	Alias             string
	HumanReadableName string
	Flags             []cli.Flag
	ProviderFunc      func(*cli.Context) (Provider, error)
}

// Register adds a backend to the registry!
func Register(alias, humanReadableName string, flags []cli.Flag, providerFunc func(*cli.Context) (Provider, error)) {
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
func NewBackendProvider(alias string, c *cli.Context) (Provider, error) {
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
