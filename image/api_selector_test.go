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
				},
				{
					Infra:    "gce",
					Language: "go",
					OS:       "linux",
					Group:    "dev",
				},
				{
					Infra:    "gce",
					Language: "ruby",
					Dist:     "precise",
					Group:    "edge",
					OS:       "linux",
				},
				{
					Infra:    "macstadium6",
					Language: "python",
					OS:       "osx",
					OsxImage: "xcode7",
				},
				{
					Infra:    "macstadium6",
					Language: "objective-c",
					OS:       "osx",
					OsxImage: "xcode6.4",
				},
				{
					Infra:    "macstadium6",
					Language: "node_js",
					OsxImage: "xcode6.1",
					Dist:     "yosammity",
					Group:    "fancy",
					OS:       "osx",
				},
			},
			E: [][]*tagSet{
				{
					&tagSet{[]string{"language_ruby:true", "os:linux"}, false},
					&tagSet{[]string{"language_ruby:true", "os:linux"}, false},
					&tagSet{[]string{"language_ruby:true"}, true},
					&tagSet{[]string{"os:linux"}, true},
				},
				{
					&tagSet{[]string{"group:dev", "language_go:true", "os:linux"}, false},
					&tagSet{[]string{"group:dev", "language_go:true"}, false},
					&tagSet{[]string{"language_go:true", "os:linux"}, false},
					&tagSet{[]string{"language_go:true"}, true},
					&tagSet{[]string{"group:dev"}, true},
					&tagSet{[]string{"os:linux"}, true},
				},
				{
					&tagSet{[]string{"dist:precise", "group:edge", "language_ruby:true", "os:linux"}, false},
					&tagSet{[]string{"dist:precise", "group:edge", "language_ruby:true"}, false},
					&tagSet{[]string{"dist:precise", "language_ruby:true"}, false},
					&tagSet{[]string{"group:edge", "language_ruby:true"}, false},
					&tagSet{[]string{"language_ruby:true", "os:linux"}, false},
					&tagSet{[]string{"language_ruby:true"}, true},
					&tagSet{[]string{"dist:precise"}, true},
					&tagSet{[]string{"group:edge"}, true},
					&tagSet{[]string{"os:linux"}, true},
				},
				{
					&tagSet{[]string{"language_python:true", "os:osx", "osx_image:xcode7"}, false},
					&tagSet{[]string{"language_python:true", "osx_image:xcode7"}, false},
					&tagSet{[]string{"language_python:true", "os:osx"}, false},
					&tagSet{[]string{"language_python:true"}, true},
					&tagSet{[]string{"osx_image:xcode7"}, true},
					&tagSet{[]string{"os:osx"}, true},
				},
				{
					&tagSet{[]string{"language_objective-c:true", "os:osx", "osx_image:xcode6.4"}, false},
					&tagSet{[]string{"language_objective-c:true", "osx_image:xcode6.4"}, false},
					&tagSet{[]string{"language_objective-c:true", "os:osx"}, false},
					&tagSet{[]string{"language_objective-c:true"}, true},
					&tagSet{[]string{"osx_image:xcode6.4"}, true},
					&tagSet{[]string{"os:osx"}, true},
				},
				{
					&tagSet{[]string{"dist:yosammity", "group:fancy", "language_node_js:true", "os:osx", "osx_image:xcode6.1"}, false},
					&tagSet{[]string{"language_node_js:true", "osx_image:xcode6.1"}, false},
					&tagSet{[]string{"dist:yosammity", "group:fancy", "language_node_js:true"}, false},
					&tagSet{[]string{"dist:yosammity", "language_node_js:true"}, false},
					&tagSet{[]string{"group:fancy", "language_node_js:true"}, false},
					&tagSet{[]string{"language_node_js:true", "os:osx"}, false},
					&tagSet{[]string{"language_node_js:true"}, true},
					&tagSet{[]string{"osx_image:xcode6.1"}, true},
					&tagSet{[]string{"dist:yosammity"}, true},
					&tagSet{[]string{"group:fancy"}, true},
					&tagSet{[]string{"os:osx"}, true},
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
