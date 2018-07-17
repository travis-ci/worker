package image

import (
	"strings"

	"github.com/travis-ci/worker/config"
)

// EnvSelector implements Selector for environment-based mappings
type EnvSelector struct {
	c *config.ProviderConfig

	lookup map[string]string
}

// NewEnvSelector builds a new EnvSelector from the given *config.ProviderConfig
func NewEnvSelector(c *config.ProviderConfig) (*EnvSelector, error) {
	es := &EnvSelector{c: c}
	es.buildLookup()
	return es, nil
}

func (es *EnvSelector) buildLookup() {
	lookup := map[string]string{}

	es.c.Each(func(key, value string) {
		if strings.HasPrefix(key, "IMAGE_") {
			lookup[strings.ToLower(strings.Replace(key, "IMAGE_", "", -1))] = value
		}
	})

	es.lookup = lookup
}

func (es *EnvSelector) Select(params *Params) (string, error) {
	imageName := "default"

	for _, key := range es.buildCandidateKeys(params) {
		if key == "" {
			continue
		}

		if s, ok := es.lookup[key]; ok {
			imageName = s
			break
		}
	}

	// check for one level of indirection
	if selected, ok := es.lookup[imageName]; ok {
		return selected, nil
	}

	return imageName, nil
}

func (es *EnvSelector) buildCandidateKeys(params *Params) []string {
	fullKey := []string{}
	candidateKeys := []string{}

	addKey := func(key string, addToFull bool) {
		if addToFull {
			fullKey = append(fullKey, key)
		}
		candidateKeys = append(candidateKeys, key)
	}

	hasLang := params.Language != ""
	needsOS := true
	needsLangSuffix := false

	if params.OS == "osx" && params.OsxImage != "" && hasLang {
		addKey("osx_image_"+params.OsxImage, true)
		needsOS = false
		needsLangSuffix = true
	}

	if params.Dist != "" && hasLang {
		addKey("dist_"+params.Dist, true)
		needsLangSuffix = true
	}

	if params.Group != "" && hasLang {
		addKey("group_"+params.Group+"_"+params.Language, false)
		addKey("group_"+params.Group, true)
		needsLangSuffix = true
	}

	if params.OS != "" && hasLang {
		if needsOS {
			addKey(params.OS, true)
			addKey("os_"+params.OS, false)
		}
		needsLangSuffix = true
	}

	if hasLang {
		if needsLangSuffix {
			addKey(params.Language, true)
			addKey("language_"+params.Language, false)
		} else {
			addKey("language_"+params.Language, true)
		}
	}

	if params.OS == "osx" && params.OsxImage != "" {
		addKey("osx_image_"+params.OsxImage, false)
	}

	if params.Dist != "" {
		addKey("dist_"+params.Dist, false)
	}

	if params.Group != "" {
		addKey("group_"+params.Group, false)
	}

	if params.OS != "" {
		addKey(params.OS, false)
		addKey("default_"+params.OS, false)
		addKey(params.OS, false)
	}

	return append([]string{strings.Join(fullKey, "_")}, candidateKeys...)
}
