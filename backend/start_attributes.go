package backend

// StartAttributes contains some parts of the config which can be used to
// determine the type of instance to boot up (for example, what image to use)
type StartAttributes struct {
	Language string `json:"language"`
	OsxImage string `json:"osx_image"`
	Dist     string `json:"dist"`
	Group    string `json:"group"`
	OS       string `json:"os"`
}

// SetDefaults sets any missing required attributes to the default values provided
func (sa *StartAttributes) SetDefaults(lang, dist, group, os string) {
	if sa.Language == "" {
		sa.Language = lang
	}

	if sa.Dist == "" {
		sa.Dist = dist
	}

	if sa.Group == "" {
		sa.Group = group
	}

	if sa.OS == "" {
		sa.OS = os
	}
}
