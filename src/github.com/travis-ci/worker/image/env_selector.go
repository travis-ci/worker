package image

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/travis-ci/worker/config"
)

var (
	nonAlphaNumRegexp = regexp.MustCompile(`[^a-zA-Z0-9_]+`)
)

// EnvSelector implements Selector for environment-based mappings
type EnvSelector struct {
	c *config.ProviderConfig

	imageAliases map[string]string
}

// NewEnvSelector builds a new EnvSelector from the given *config.ProviderConfig
func NewEnvSelector(c *config.ProviderConfig) *EnvSelector {
	es := &EnvSelector{c: c}
	es.buildImageAliasMap()
	return es
}

func (es *EnvSelector) buildImageAliasMap() {
	aliasNames := es.c.Get("IMAGE_ALIASES")

	aliasNamesSlice := strings.Split(aliasNames, ",")

	imageAliases := map[string]string{}

	es.c.Each(func(key, value string) {
		if strings.HasPrefix(key, "IMAGE_") {
			imageAliases[strings.ToLower(strings.Replace(key, "IMAGE_", "", -1))] = value
		}
	})

	for _, aliasName := range aliasNamesSlice {
		normalizedAliasName := strings.ToUpper(string(nonAlphaNumRegexp.ReplaceAll([]byte(aliasName), []byte("_"))))

		key := fmt.Sprintf("IMAGE_ALIAS_%s", normalizedAliasName)
		if !es.c.IsSet(key) {
			// TODO: warn?
			continue
		}

		imageAliases[aliasName] = es.c.Get(key)
	}

	es.imageAliases = imageAliases
}

func (es *EnvSelector) Select(params *Params) (string, error) {
	imageName := "default"

	for _, key := range []string{
		params.Language,
		fmt.Sprintf("dist_%s_%s", params.Dist, params.Language),
		fmt.Sprintf("dist_%s", params.Dist),
		fmt.Sprintf("group_%s_%s", params.Group, params.Language),
		fmt.Sprintf("group_%s", params.Group),
		fmt.Sprintf("osx_image_%s_%s", params.OsxImage, params.Language),
		fmt.Sprintf("osx_image_%s", params.OsxImage),
		fmt.Sprintf("os_%s_%s", params.OS, params.Language),
		fmt.Sprintf("os_%s", params.OS),
		fmt.Sprintf("language_%s", params.Language),
		fmt.Sprintf("default_%s", params.OS),
		params.OS,
	} {
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
