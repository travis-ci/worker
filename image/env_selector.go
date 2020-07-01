package image

import (
	gocontext "context"
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
			lookup[strings.ToLower(strings.Replace(key, "IMAGE_", "", 1))] = value
		}
	})

	es.lookup = lookup
}

func (es *EnvSelector) Select(ctx gocontext.Context, params *Params) (string, error) {
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

	hasLang := params.Language != ""
	hasDist := params.Dist != ""
	hasGroup := params.Group != ""
	hasOS := params.OS != ""

	if params.OS == "osx" && params.OsxImage != "" {
		if hasLang {
			candidateKeys = append(candidateKeys, "osx_image_"+params.OsxImage+"_"+params.Language)
		}
		candidateKeys = append(candidateKeys, "osx_image_"+params.OsxImage)
	}

	if hasDist && hasGroup && hasLang {
		candidateKeys = append(candidateKeys, "dist_"+params.Dist+"_group_"+params.Group+"_"+params.Language)
		candidateKeys = append(candidateKeys, params.Dist+"_"+params.Group+"_"+params.Language)
	}

	if hasDist && hasLang {
		candidateKeys = append(candidateKeys, "dist_"+params.Dist+"_"+params.Language)
		candidateKeys = append(candidateKeys, params.Dist+"_"+params.Language)
	}

	if hasGroup && hasLang {
		candidateKeys = append(candidateKeys, "group_"+params.Group+"_"+params.Language)
		candidateKeys = append(candidateKeys, params.Group+"_"+params.Language)
	}

	if hasOS && hasLang {
		candidateKeys = append(candidateKeys, "os_"+params.OS+"_"+params.Language)
		candidateKeys = append(candidateKeys, params.OS+"_"+params.Language)
	}

	if hasDist {
		candidateKeys = append(candidateKeys, "default_dist_"+params.Dist)
		candidateKeys = append(candidateKeys, "dist_"+params.Dist)
		candidateKeys = append(candidateKeys, params.Dist)
	}

	if hasGroup {
		candidateKeys = append(candidateKeys, "default_group_"+params.Group)
		candidateKeys = append(candidateKeys, "group_"+params.Group)
		candidateKeys = append(candidateKeys, params.Group)
	}

	if hasLang {
		candidateKeys = append(candidateKeys, "language_"+params.Language)
		candidateKeys = append(candidateKeys, params.Language)
		candidateKeys = append(candidateKeys, params.Language)
	}

	if hasOS {
		candidateKeys = append(candidateKeys, "default_os_"+params.OS)
		candidateKeys = append(candidateKeys, "os_"+params.OS)
		candidateKeys = append(candidateKeys, params.OS)
	}

	return append([]string{strings.Join(fullKey, "_")}, candidateKeys...)
}
