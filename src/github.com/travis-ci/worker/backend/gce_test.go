package backend

import (
	"testing"

	"github.com/travis-ci/worker/config"
)

var (
	gceTestProvider *gceProvider
)

func gceTestSetup(t *testing.T, cfg *config.ProviderConfig) {
	p, err := newGCEProvider(cfg)
	if err != nil {
		t.Error(err)
	}

	gceTestProvider = p.(*gceProvider)
}

func gceTestTeardown() {
	gceTestProvider = nil
}

func TestGCEStart(t *testing.T) {
	gceTestSetup(t, config.ProviderConfigFromMap(map[string]string{
		"ACCOUNT_JSON": "account_json",
	}))

	defer gceTestTeardown()
}
