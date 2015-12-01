package image

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	testAPIServerString = `
{
	"data": [
		{
			"id": 1,
			"infra": "test",
			"name": "travis-ci-awesome"
		}
	]
}
`
	testAPIServerEmptyResponseString = `{"data": []}`
)

var (
	testAPITagTestCases = []struct {
		P []*Params
		E [][]*tagSet
	}{
		{
			P: []*Params{
				{
					Infra:    "gce",
					Language: "ruby",
					OS:       "linux",
					JobID:    uint64(4),
					Repo:     "corp/frob",
				},
				{
					Infra:    "gce",
					Language: "go",
					OS:       "linux",
					Group:    "dev",
					JobID:    uint64(4),
					Repo:     "corp/frob",
				},
				{
					Infra:    "gce",
					Language: "ruby",
					Dist:     "precise",
					Group:    "edge",
					OS:       "linux",
					JobID:    uint64(4),
					Repo:     "corp/frob",
				},
				{
					Infra:    "macstadium6",
					Language: "python",
					OS:       "osx",
					OsxImage: "xcode7",
					JobID:    uint64(4),
					Repo:     "corp/frob",
				},
				{
					Infra:    "macstadium6",
					Language: "objective-c",
					OS:       "osx",
					OsxImage: "xcode6.4",
					JobID:    uint64(4),
					Repo:     "corp/frob",
				},
				{
					Infra:    "macstadium6",
					Language: "node_js",
					OsxImage: "xcode6.1",
					Dist:     "yosammity",
					Group:    "fancy",
					OS:       "osx",
					JobID:    uint64(4),
					Repo:     "corp/frob",
				},
			},
			E: [][]*tagSet{
				{
					&tagSet{[]string{"language_ruby:true", "os:linux"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_ruby:true", "os:linux"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_ruby:true"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"os:linux"}, true, uint64(4), "corp/frob"},
				},
				{
					&tagSet{[]string{"group:dev", "language_go:true", "os:linux"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"group:dev", "language_go:true"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_go:true", "os:linux"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_go:true"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"group:dev"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"os:linux"}, true, uint64(4), "corp/frob"},
				},
				{
					&tagSet{[]string{"dist:precise", "group:edge", "language_ruby:true", "os:linux"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"dist:precise", "group:edge", "language_ruby:true"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"dist:precise", "language_ruby:true"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"group:edge", "language_ruby:true"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_ruby:true", "os:linux"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_ruby:true"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"dist:precise"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"group:edge"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"os:linux"}, true, uint64(4), "corp/frob"},
				},
				{
					&tagSet{[]string{"language_python:true", "os:osx", "osx_image:xcode7"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_python:true", "osx_image:xcode7"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_python:true", "os:osx"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_python:true"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"osx_image:xcode7"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"os:osx"}, true, uint64(4), "corp/frob"},
				},
				{
					&tagSet{[]string{"language_objective-c:true", "os:osx", "osx_image:xcode6.4"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_objective-c:true", "osx_image:xcode6.4"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_objective-c:true", "os:osx"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_objective-c:true"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"osx_image:xcode6.4"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"os:osx"}, true, uint64(4), "corp/frob"},
				},
				{
					&tagSet{[]string{"dist:yosammity", "group:fancy", "language_node_js:true", "os:osx", "osx_image:xcode6.1"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_node_js:true", "osx_image:xcode6.1"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"dist:yosammity", "group:fancy", "language_node_js:true"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"dist:yosammity", "language_node_js:true"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"group:fancy", "language_node_js:true"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_node_js:true", "os:osx"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_node_js:true"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"osx_image:xcode6.1"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"dist:yosammity"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"group:fancy"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"os:osx"}, true, uint64(4), "corp/frob"},
				},
			},
		},
	}
)

func TestNewAPISelector(t *testing.T) {
	u, _ := url.Parse("https://foo:bar@whatever.example.com/images")
	assert.NotNil(t, NewAPISelector(u))
}

func TestAPISelector_Select(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, testAPIServerString)
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)

	as := NewAPISelector(u)

	actual, _ := as.Select(&Params{
		Infra:    "test",
		Language: "ruby",
		OsxImage: "meow",
		Dist:     "yosamitty",
		Group:    "dev",
		OS:       "osx",
	})
	assert.Equal(t, actual, "travis-ci-awesome")
}

func TestAPISelector_SelectDefault(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, testAPIServerEmptyResponseString)
	}))
	defer ts.Close()
	u, _ := url.Parse(ts.URL)
	actual, _ := NewAPISelector(u).Select(&Params{})
	assert.Equal(t, actual, "default")
}

func TestAPISelector_SelectDefaultWhenBadResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	u, _ := url.Parse(ts.URL)
	actual, _ := NewAPISelector(u).Select(&Params{})
	assert.Equal(t, actual, "default")
}

func TestAPISelector_SelectDefaultWhenBadJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, `{"data`)
	}))
	defer ts.Close()
	u, _ := url.Parse(ts.URL)
	actual, _ := NewAPISelector(u).Select(&Params{})
	assert.Equal(t, actual, "default")
}

func TestAPISelector_buildCandidateTags(t *testing.T) {
	as := NewAPISelector(nil)

	for _, tc := range testAPITagTestCases {
		for i, params := range tc.P {
			assert.Equal(t, tc.E[i], as.buildCandidateTags(params))
		}
	}
}
