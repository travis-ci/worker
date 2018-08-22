package backend

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/travis-ci/worker/config"
)

var (
	backendRegistry      = map[string]*Backend{}
	backendRegistryMutex sync.Mutex
)

var ErrDownloadTraceNotImplemented = errors.New("DownloadTrace not implemented")

// Backend wraps up an alias, backend provider help, and a factory func for a
// given backend provider wheee
type Backend struct {
	Alias             string
	HumanReadableName string
	ProviderHelp      map[string]string
	ProviderFunc      func(*config.ProviderConfig) (Provider, error)
}

// Register adds a backend to the registry!
func Register(alias, humanReadableName string, providerHelp map[string]string, providerFunc func(*config.ProviderConfig) (Provider, error)) {
	backendRegistryMutex.Lock()
	defer backendRegistryMutex.Unlock()

	backendRegistry[alias] = &Backend{
		Alias:             alias,
		HumanReadableName: humanReadableName,
		ProviderHelp:      providerHelp,
		ProviderFunc:      providerFunc,
	}
}

// NewBackendProvider looks up a backend by its alias and returns a provider via
// the factory func on the registered *Backend
func NewBackendProvider(alias string, cfg *config.ProviderConfig) (Provider, error) {
	backendRegistryMutex.Lock()
	defer backendRegistryMutex.Unlock()

	backend, ok := backendRegistry[alias]
	if !ok {
		return nil, fmt.Errorf("unknown backend provider: %s", alias)
	}

	return backend.ProviderFunc(cfg)
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
