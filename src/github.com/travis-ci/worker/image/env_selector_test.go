package image

import (
	"fmt"
	"strings"
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
				"IMAGE_ALIASES": strings.Join([]string{
					"language_haskell",
					"language_java",
				}, ","),
				"IMAGE_ALIAS_LANGUAGE_HASKELL": "language_ruby",
				"IMAGE_ALIAS_LANGUAGE_JAVA":    "legacy",
				"IMAGE_LANGUAGE_RUBY":          "travis-ci-ruby-9001",
				"IMAGE_LEGACY":                 "travis-ci-legacy-00",
				"IMAGE_DEFAULT":                "travis-ci-default",
			},
			O: []*testEnvCase{
				{E: "travis-ci-ruby-9001", P: &Params{Language: "haskell"}},
				{E: "travis-ci-legacy-00", P: &Params{Language: "java"}},
				{E: "travis-ci-default", P: &Params{Language: "clojure"}},
				{E: "travis-ci-ruby-9001", P: &Params{Language: "ruby"}},
			},
		},
		{
			E: map[string]string{
				"IMAGE_ALIASES": strings.Join([]string{
					"dist_trusty_ruby",
					"dist_trusty",
				}, ","),
				"IMAGE_ALIAS_DIST_TRUSTY":      "trusty",
				"IMAGE_ALIAS_DIST_TRUSTY_RUBY": "travis-ci-ruby",
				"IMAGE_TRUSTY":                 "travis-ci-mega",
			},
			O: []*testEnvCase{
				{E: "travis-ci-mega", P: &Params{Dist: "trusty"}},
				{E: "travis-ci-ruby", P: &Params{Dist: "trusty", Language: "ruby"}},
			},
		},
		{
			E: map[string]string{
				"IMAGE_ALIASES": strings.Join([]string{
					"group_dev",
					"group_dev_go",
					"dist_trusty",
					"osx_image_xcode6.4",
					"osx_image_xcode7_swift",
					"os_linux",
					"linux_java",
					"language_haskell",
					"default_solaris",
				}, ","),
				"IMAGE_ALIAS_GROUP_DEV":              "vivid",
				"IMAGE_ALIAS_GROUP_DEV_GO":           "utopic",
				"IMAGE_ALIAS_DIST_TRUSTY":            "trusty",
				"IMAGE_ALIAS_OSX_IMAGE_XCODE6_4":     "xcode6",
				"IMAGE_ALIAS_OSX_IMAGE_XCODE7_SWIFT": "xcode7_swift",
				"IMAGE_ALIAS_OS_LINUX":               "trusty",
				"IMAGE_ALIAS_LINUX_JAVA":             "utopic",
				"IMAGE_ALIAS_LANGUAGE_HASKELL":       "trusty",
				"IMAGE_ALIAS_DEFAULT_SOLARIS":        "smartos",
				"IMAGE_DEFAULT_OSX":                  "xcode7",
				"IMAGE_TRUSTY":                       "travis-ci-mega",
				"IMAGE_XCODE7":                       "travis-xcode7b4",
				"IMAGE_XCODE7_SWIFT":                 "travis-xcode7b6",
				"IMAGE_SMARTOS":                      "base64-20150902",
			},
			O: []*testEnvCase{
				{E: "travis-ci-mega", P: &Params{OS: "linux", Dist: "trusty"}},
				{E: "travis-ci-mega", P: &Params{OS: "linux", Dist: "trusty", Language: "ruby"}},
				{E: "travis-xcode7b6", P: &Params{OS: "osx", OsxImage: "xcode7", Language: "swift"}},
				{E: "travis-xcode7b4", P: &Params{OS: "osx", Language: "objective-c"}},
				{E: "base64-20150902", P: &Params{OS: "solaris", Language: "ruby"}},
				{E: "base64-20150902", P: &Params{OS: "smartos"}},
				{E: "utopic", P: &Params{OS: "linux", Language: "java"}},
				{E: "vivid", P: &Params{OS: "linux", Group: "dev", Language: "java"}},
				{E: "utopic", P: &Params{OS: "linux", Group: "dev", Language: "go"}},
				{E: "travis-ci-mega", P: &Params{OS: "linux", Language: "haskell"}},
				{E: "xcode6", P: &Params{OS: "osx", OsxImage: "xcode6.4"}},
			},
		},
	}
)

type testEnvCase struct {
	E string
	P *Params
}

func TestNewEnvSelector(t *testing.T) {
	assert.Panics(t, func() { NewEnvSelector(nil) })
	assert.NotPanics(t, func() {
		NewEnvSelector(config.ProviderConfigFromMap(map[string]string{}))
	})
}

func TestEnvSelector_Select(t *testing.T) {
	for _, tesm := range testEnvSelectorMaps {
		es, err := NewEnvSelector(config.ProviderConfigFromMap(tesm.E))
		if err != nil {
			t.Fatal(err)
		}

		for _, tc := range tesm.O {
			actual, _ := es.Select(tc.P)
			assert.Equal(t, tc.E, actual, fmt.Sprintf("%#v %q", tc.P, tc.E))
		}
	}
}
