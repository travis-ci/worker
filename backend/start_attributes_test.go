package backend

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

var (
	testStartAttributesTestCases = []struct {
		A []*StartAttributes
		O []*StartAttributes
	}{
		{
			A: []*StartAttributes{
				{Language: ""},
				{Language: "ruby"},
				{Language: "python", Dist: "trusty"},
				{Language: "python", Dist: "trusty", Arch:"amd64", Group: "edge"},
				{Language: "python", Dist: "frob", Arch:"amd64", Group: "edge", OS: "flob"},
				{Language: "python", Dist: "frob", Arch:"amd64", OsxImage: "", Group: "edge", OS: "flob"},
				{Language: "python", Dist: "frob", Arch:"amd64", OsxImage: "", Group: "edge", OS: "flob", VMType: "premium"},
				{Language: "python", Dist: "frob", Arch:"amd64", OsxImage: "", Group: "edge", OS: "flob", VMType: "premium", VMConfig: VmConfig{GpuCount: 0, GpuType: "", Zone: ""}},
			},
			O: []*StartAttributes{
				{Language: "default", Dist: "precise", Arch:"amd64", Group: "stable", OS: "linux", VMType: "default"},
				{Language: "ruby", Dist: "precise", Arch:"amd64", Group: "stable", OS: "linux", VMType: "default"},
				{Language: "python", Dist: "trusty", Arch:"amd64", Group: "stable", OS: "linux", VMType: "default"},
				{Language: "python", Dist: "trusty", Arch:"amd64", Group: "edge", OS: "linux", VMType: "default"},
				{Language: "python", Dist: "frob", Arch:"amd64", Group: "edge", OS: "flob", VMType: "default"},
				{Language: "python", Dist: "frob", Arch:"amd64", OsxImage: "", Group: "edge", OS: "flob", VMType: "default"},
				{Language: "python", Dist: "frob", Arch:"amd64", OsxImage: "", Group: "edge", OS: "flob", VMType: "premium"},
				{Language: "python", Dist: "frob", Arch:"amd64", OsxImage: "", Group: "edge", OS: "flob", VMType: "premium", VMConfig: VmConfig{}},
			},
		},
	}
)

func TestStartAttributes(t *testing.T) {
	sa := &StartAttributes{}
	assert.Equal(t, "", sa.Dist)
	assert.Equal(t, "", sa.Arch)
	assert.Equal(t, "", sa.Group)
	assert.Equal(t, "", sa.Language)
	assert.Equal(t, "", sa.OS)
	assert.Equal(t, "", sa.OsxImage)
	assert.Equal(t, "", sa.VMType)
	assert.Equal(t, VmConfig{GpuCount: 0, GpuType: "", Zone: ""}, sa.VMConfig)
}

func TestStartAttributes_SetDefaults(t *testing.T) {
	for _, tc := range testStartAttributesTestCases {
		for i, sa := range tc.A {
			expected := tc.O[i]
			sa.SetDefaults("default", "precise", "amd64", "stable", "linux", "default", sa.VMConfig)
			assert.Equal(t, expected, sa)
		}
	}
}
