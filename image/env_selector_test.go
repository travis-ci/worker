package image

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/travis-ci/worker/config"
)

var (
	testEnvSelectorMaps = []*struct {
		E map[string]string
		O []*testEnvCase
	}{
		{
			E: map[string]string{
				"IMAGE_DEFAULT":            "travis:default",
				"IMAGE_LINUX":              "travis:linux",
				"IMAGE_DIST_XENIAL":        "travis:xenial",
				"IMAGE_DIST_XENIAL_PYTHON": "travis:py3k",
				"IMAGE_GROUP_EDGE":         "travis:edge",
				"IMAGE_GROUP_EDGE_RUBY":    "travis:ruby9001",
				"IMAGE_LANGUAGE_RUBY":      "travis:ruby8999",
				"IMAGE_PYTHON":             "travis:python",
			},
			O: []*testEnvCase{
				{E: "travis:default", P: &Params{Language: "clojure"}},
				{E: "travis:default", P: &Params{Language: "java"}},
				{E: "travis:edge", P: &Params{Language: "java", Group: "edge"}},
				{E: "travis:linux", P: &Params{Language: "bf", Group: "wat", OS: "linux"}},
				{E: "travis:py3k", P: &Params{Language: "python", Dist: "xenial"}},
				{E: "travis:ruby8999", P: &Params{Language: "ruby"}},
				{E: "travis:ruby9001", P: &Params{Language: "ruby", Group: "edge"}},
				{E: "travis:xenial", P: &Params{Language: "java", Dist: "xenial"}},
			},
		},
		{
			E: map[string]string{
				"IMAGE_DEFAULT":           "travisci/ci-garnet:packer-1410230255-fafafaf",
				"IMAGE_DIST_BIONIC":       "registry.business.com/fancy/ubuntu:bionic",
				"IMAGE_DIST_TRUSTY":       "travisci/ci-connie:packer-1420290255-fafafaf",
				"IMAGE_DIST_TRUSTY_RUBY":  "registry.business.com/travisci/ci-ruby:whatever",
				"IMAGE_GROUP_EDGE_PYTHON": "travisci/ci-garnet:packer-1530230255-fafafaf",
				"IMAGE_LANGUAGE_RUBY":     "registry.business.com/travisci/ci-ruby:whatever",
			},
			O: []*testEnvCase{
				{E: "registry.business.com/fancy/ubuntu:bionic", P: &Params{Language: "bash", Dist: "bionic"}},
				{E: "registry.business.com/fancy/ubuntu:bionic", P: &Params{Language: "ruby", Dist: "bionic"}},
				{E: "registry.business.com/travisci/ci-ruby:whatever", P: &Params{Language: "ruby", Dist: "trusty"}},
				{E: "registry.business.com/travisci/ci-ruby:whatever", P: &Params{Language: "ruby"}},
				{E: "travisci/ci-connie:packer-1420290255-fafafaf", P: &Params{Language: "wat", Dist: "trusty"}},
				{E: "travisci/ci-garnet:packer-1410230255-fafafaf", P: &Params{Language: "python", Group: "stable"}},
				{E: "travisci/ci-garnet:packer-1410230255-fafafaf", P: &Params{Language: "python"}},
				{E: "travisci/ci-garnet:packer-1410230255-fafafaf", P: &Params{Language: "wat"}},
				{E: "travisci/ci-garnet:packer-1530230255-fafafaf", P: &Params{Language: "python", Group: "edge"}},
			},
		},
	}
)

type testEnvCase struct {
	E string
	P *Params
}

func TestNewEnvSelector(t *testing.T) {
	assert.Panics(t, func() { _, _ = NewEnvSelector(nil) })
	assert.NotPanics(t, func() {
		_, _ = NewEnvSelector(config.ProviderConfigFromMap(map[string]string{}))
	})
}

func TestEnvSelector_Select(t *testing.T) {
	for _, tesm := range testEnvSelectorMaps {
		es, err := NewEnvSelector(config.ProviderConfigFromMap(tesm.E))
		if err != nil {
			t.Fatal(err)
		}

		for _, tc := range tesm.O {
			actual, _ := es.Select(context.TODO(), tc.P)
			assert.Equal(t, tc.E, actual, fmt.Sprintf("%#v %q", tc.P, tc.E))
		}
	}
}
