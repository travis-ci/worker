package image

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	nonAlphaNumRegexp = regexp.MustCompile(`[^a-zA-Z0-9_]+`)
)

// EnvSelector implements Selector for environment-based mappings
type EnvSelector struct {
	rawImages       map[string]string
	rawImageAliases map[string]string

	imageAliases map[string]string
}

// NewEnvSelector builds a new EnvSelector from the given images and aliases
func NewEnvSelector(images, imageAliases map[string]string) (*EnvSelector, error) {
	es := &EnvSelector{rawImages: images, rawImageAliases: imageAliases}
	err := es.buildImageAliasMap()
	if err != nil {
		return nil, err
	}
	return es, nil
}

func (es *EnvSelector) buildImageAliasMap() error {
	imageAliases := map[string]string{}

	for k, v := range es.rawImages {
		imageAliases[k] = v
	}

	for k, v := range es.rawImageAliases {
		value, ok := imageAliases[v]
		if ok {
			imageAliases[k] = value
		}

		imageAliases[k] = v
	}

	fmt.Fprintf(os.Stderr, "IMAGE ALIASES: %#v\n", imageAliases)
	es.imageAliases = imageAliases
	return nil
}

func (es *EnvSelector) Select(params *Params) (string, error) {
	imageName := "default"

	for _, key := range es.buildCandidateKeys(params) {
		if key == "" {
			continue
		}

		if s, ok := es.imageAliases[key]; ok {
			imageName = s
			break
		}
	}

	if selected, ok := es.imageAliases[imageName]; ok {
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
