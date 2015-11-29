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
				{Language: "python", Dist: "trusty", Group: "edge"},
				{Language: "python", Dist: "frob", Group: "edge", OS: "flob"},
				{Language: "python", Dist: "frob", OsxImage: "", Group: "edge", OS: "flob"},
			},
			O: []*StartAttributes{
				{Language: "default", Dist: "precise", Group: "stable", OS: "linux"},
				{Language: "ruby", Dist: "precise", Group: "stable", OS: "linux"},
				{Language: "python", Dist: "trusty", Group: "stable", OS: "linux"},
				{Language: "python", Dist: "trusty", Group: "edge", OS: "linux"},
				{Language: "python", Dist: "frob", Group: "edge", OS: "flob"},
				{Language: "python", Dist: "frob", OsxImage: "", Group: "edge", OS: "flob"},
			},
		},
	}
)

func TestStartAttributes(t *testing.T) {
	sa := &StartAttributes{}
	assert.Equal(t, "", sa.Dist)
	assert.Equal(t, "", sa.Group)
	assert.Equal(t, "", sa.Language)
	assert.Equal(t, "", sa.OS)
	assert.Equal(t, "", sa.OsxImage)
}

func TestStartAttributes_SetDefaults(t *testing.T) {
	for _, tc := range testStartAttributesTestCases {
		for i, sa := range tc.A {
			expected := tc.O[i]
			sa.SetDefaults("default", "precise", "stable", "linux")
			assert.Equal(t, expected, sa)
		}
	}
}
