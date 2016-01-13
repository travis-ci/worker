package image

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

var (
	testEnvSelectorMaps = []*struct {
		I map[string]string
		A map[string]string
		O []*testEnvCase
	}{
		{
			I: map[string]string{
				"language_ruby": "travis-ci-ruby-9001",
				"legacy":        "travis-ci-legacy-00",
				"default":       "travis-ci-default",
			},
			A: map[string]string{
				"language_haskell": "language_ruby",
				"language_java":    "legacy",
			},
			O: []*testEnvCase{
				{E: "travis-ci-ruby-9001", P: &Params{Language: "haskell"}},
				{E: "travis-ci-legacy-00", P: &Params{Language: "java"}},
				{E: "travis-ci-default", P: &Params{Language: "clojure"}},
				{E: "travis-ci-ruby-9001", P: &Params{Language: "ruby"}},
			},
		},
		{
			I: map[string]string{
				"trusty": "travis-ci-mega",
			},
			A: map[string]string{
				"dist_trusty":      "trusty",
				"dist_trusty_ruby": "travis-ci-ruby",
			},
			O: []*testEnvCase{
				{E: "travis-ci-mega", P: &Params{Dist: "trusty"}},
				{E: "travis-ci-ruby", P: &Params{Dist: "trusty", Language: "ruby"}},
			},
		},
		{
			I: map[string]string{
				"default_osx":  "xcode7",
				"trusty":       "travis-ci-mega",
				"xcode7":       "travis-xcode7b4",
				"xcode7_swift": "travis-xcode7b6",
				"smartos":      "base64-20150902",
			},
			A: map[string]string{
				"group_dev":              "vivid",
				"group_dev_go":           "utopic",
				"dist_trusty":            "trusty",
				"osx_image_xcode6.4":     "xcode6",
				"osx_image_xcode7_swift": "xcode7_swift",
				"os_linux":               "trusty",
				"linux_java":             "utopic",
				"language_haskell":       "trusty",
				"default_solaris":        "smartos",
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
	assert.NotPanics(t, func() {
		NewEnvSelector(map[string]string{}, map[string]string{})
	})
}

func TestEnvSelector_Select(t *testing.T) {
	for _, tesm := range testEnvSelectorMaps {
		es, err := NewEnvSelector(tesm.I, tesm.A)
		if err != nil {
			t.Fatal(err)
		}

		for _, tc := range tesm.O {
			actual, _ := es.Select(tc.P)
			assert.Equal(t, tc.E, actual, fmt.Sprintf("E: %q P: %#v", tc.E, tc.P))
		}
	}
}
