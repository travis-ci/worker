package config

import (
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/codegangsta/cli"
)

var (
	cliHelpPrinter   = cli.HelpPrinter
	providerHelps    = map[string]string{}
	providerHelpsMut sync.Mutex
)

const (
	providerHelpHeader = `
All provider options must be given as environment variables of the form:

   $[TRAVIS_WORKER_]{UPCASE_PROVIDER_NAME}_{UPCASE_UNDERSCORED_KEY}
     ^------------^
   optional namespace

e.g.:

   TRAVIS_WORKER_DOCKER_HOST='tcp://127.0.0.1:4243'
   TRAVIS_WORKER_DOCKER_PRIVILEGED='true'

`
)

// SetProviderHelp sets the help for a backend.Provider with the given name.
// The help text should contain a list of all required and optional
// configuration options.
func SetProviderHelp(providerName, help string) {
	providerHelpsMut.Lock()
	defer providerHelpsMut.Unlock()

	providerHelps[providerName] = help
}

func init() {
	cli.HelpPrinter = helpPrinter
}

func helpPrinter(w io.Writer, templ string, data interface{}) {
	cliHelpPrinter(w, templ, data)

	providerNames := []string{}
	for providerName, _ := range providerHelps {
		providerNames = append(providerNames, providerName)
	}

	sort.Strings(providerNames)

	if len(providerNames) == 0 {
		return
	}

	fmt.Fprintf(w, providerHelpHeader)

	for _, providerName := range providerNames {
		fmt.Fprintf(w, "%s provider help:\n", providerName)
		fmt.Fprintf(w, providerHelps[providerName])
	}
}
