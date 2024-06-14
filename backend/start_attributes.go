package backend

import (
	"time"
)

type VmConfig struct {
	GpuCount int64  `json:"gpu_count"`
	GpuType  string `json:"gpu_type"`
	Zone     string `json:"zone"`
}

// StartAttributes contains some parts of the config which can be used to
// determine the type of instance to boot up (for example, what image to use)
type StartAttributes struct {
	Language  string `json:"language"`
	OsxImage  string `json:"osx_image"`
	Dist      string `json:"dist"`
	Arch      string `json:"arch"`
	Group     string `json:"group"`
	OS        string `json:"os"`
	ImageName string `json:"image_name"`
	OSCustom  string `json:"os_custom"`

	// The VMType isn't stored in the config directly, but in the top level of
	// the job payload, see the worker.JobPayload struct.
	VMType string `json:"-"`

	// The VMConfig isn't stored in the config directly, but in the top level of
	// the job payload, see the worker.JobPayload struct.
	VMConfig VmConfig `json:"-"`

	// The VMSize isn't stored in the config directly, but in the top level of
	// the job payload, see the worker.JobPayload struct.
	VMSize string `json:"-"`

	// Warmer isn't stored in the config directly, but in the top level of
	// the job payload, see the worker.JobPayload struct.
	Warmer bool `json:"-"`

	// HardTimeout isn't stored in the config directly, but is injected
	// from the processor
	HardTimeout time.Duration `json:"-"`

	// ProgressType isn't stored in the config directly, but is injected from
	// the processor
	ProgressType string `json:"-"`

	// TamToken isn't stored in the config directly, but in the top level of
	// the job payload, see the worker.JobPayload struct.
	TamToken string `json:"-"`
}

// SetDefaults sets any missing required attributes to the default values provided
func (sa *StartAttributes) SetDefaults(lang, dist, arch, group, os, vmType string, vmConfig VmConfig) {
	if sa.Language == "" {
		sa.Language = lang
	}

	if sa.Dist == "" {
		sa.Dist = dist
	}

	if sa.Arch == "" {
		sa.Arch = arch
	}

	if sa.Group == "" {
		sa.Group = group
	}

	if sa.OS == "" {
		sa.OS = os
	}

	if sa.VMType == "" {
		sa.VMType = vmType
	}

	if sa.VMConfig.GpuCount == 0 {
		sa.VMConfig.GpuCount = vmConfig.GpuCount
	}

	if sa.VMConfig.GpuType == "" {
		sa.VMConfig.GpuType = vmConfig.GpuType
	}

	if sa.VMConfig.Zone == "" {
		sa.VMConfig.Zone = vmConfig.Zone
	}
}
