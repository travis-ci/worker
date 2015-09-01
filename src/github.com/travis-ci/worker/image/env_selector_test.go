package image

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/travis-ci/worker/config"
)

var (
	testEnvSelectorMaps = []*testEnvSelectorMap{
		&testEnvSelectorMap{
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
				{Expected: "travis-ci-ruby-9001", Params: &Params{Language: "haskell"}},
				{Expected: "travis-ci-legacy-00", Params: &Params{Language: "java"}},
				{Expected: "travis-ci-default", Params: &Params{Language: "clojure"}},
				{Expected: "travis-ci-ruby-9001", Params: &Params{Language: "ruby"}},
			},
		},
		&testEnvSelectorMap{
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
				{Expected: "travis-ci-mega", Params: &Params{Dist: "trusty"}},
				{Expected: "travis-ci-ruby", Params: &Params{Dist: "trusty", Language: "ruby"}},
			},
		},
		&testEnvSelectorMap{
			E: map[string]string{
				"IMAGE_ALIASES": strings.Join([]string{
					"group_dev",
					"group_dev_go",
					"dist_trusty",
					"osx_image_xcode6.4",
					"osx_image_xcode7_swift",
					"os_linux",
					"os_linux_java",
					"language_haskell",
					"default_solaris",
				}, ","),
				"IMAGE_ALIAS_GROUP_DEV":              "vivid",
				"IMAGE_ALIAS_GROUP_DEV_GO":           "utopic",
				"IMAGE_ALIAS_DIST_TRUSTY":            "trusty",
				"IMAGE_ALIAS_OSX_IMAGE_XCODE6_4":     "xcode6",
				"IMAGE_ALIAS_OSX_IMAGE_XCODE7_SWIFT": "xcode7_swift",
				"IMAGE_ALIAS_OS_LINUX":               "trusty",
				"IMAGE_ALIAS_OS_LINUX_JAVA":          "utopic",
				"IMAGE_ALIAS_LANGUAGE_HASKELL":       "trusty",
				"IMAGE_ALIAS_DEFAULT_SOLARIS":        "smartos",
				"IMAGE_DEFAULT_OSX":                  "xcode7",
				"IMAGE_TRUSTY":                       "travis-ci-mega",
				"IMAGE_XCODE7":                       "travis-xcode7b4",
				"IMAGE_XCODE7_SWIFT":                 "travis-xcode7b6",
				"IMAGE_SMARTOS":                      "base64-20150902",
			},
			O: []*testEnvCase{
				{Expected: "travis-ci-mega", Params: &Params{Dist: "trusty"}},
				{Expected: "travis-ci-mega", Params: &Params{Dist: "trusty", Language: "ruby"}},
				{Expected: "travis-xcode7b6", Params: &Params{OsxImage: "xcode7", Language: "swift"}},
				{Expected: "travis-xcode7b4", Params: &Params{OS: "osx", Language: "objective-c"}},
				{Expected: "base64-20150902", Params: &Params{OS: "solaris", Language: "ruby"}},
				{Expected: "base64-20150902", Params: &Params{OS: "smartos"}},
				{Expected: "utopic", Params: &Params{OS: "linux", Language: "java"}},
				{Expected: "vivid", Params: &Params{Group: "dev", Language: "java"}},
				{Expected: "utopic", Params: &Params{Group: "dev", Language: "go"}},
				{Expected: "travis-ci-mega", Params: &Params{Language: "haskell"}},
				{Expected: "xcode6", Params: &Params{OsxImage: "xcode6.4"}},
			},
		},
	}
)

type testEnvSelectorMap struct {
	E map[string]string
	O []*testEnvCase
}

type testEnvCase struct {
	Expected string
	Params   *Params
}

func TestNewEnvSelector(t *testing.T) {
	assert.Panics(t, func() { NewEnvSelector(nil) })
	assert.NotPanics(t, func() {
		NewEnvSelector(config.ProviderConfigFromMap(map[string]string{}))
	})
}

func TestNewEnvSelector_Select(t *testing.T) {
	for _, tesm := range testEnvSelectorMaps {
		es := NewEnvSelector(config.ProviderConfigFromMap(tesm.E))
		for _, tc := range tesm.O {
			actual, _ := es.Select(tc.Params)
			assert.Equal(t, tc.Expected, actual, fmt.Sprintf("%#v %q", tc.Params, tc.Expected))
		}
	}
}
