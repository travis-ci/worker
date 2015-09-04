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
		E [][][]string
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
			E: [][][]string{
				{
					{"language:ruby", "language_ruby:true", "os:linux"},
					{"os:linux", "language:ruby"},
					{"os:linux", "language_ruby:true"},
					{"language:ruby"},
					{"language_ruby:true"},
					{"os:linux"},
				},
				{
					{"language:go", "language_go:true", "group:dev", "os:linux"},
					{"group:dev", "language:go"},
					{"group:dev", "language_go:true"},
					{"os:linux", "language:go"},
					{"os:linux", "language_go:true"},
					{"language:go"},
					{"language_go:true"},
					{"group:dev"},
					{"os:linux"},
				},
				{
					{"language:python", "language_python:true", "osx_image:xcode7", "os:osx"},
					{"osx_image:xcode7", "language:python"},
					{"osx_image:xcode7", "language_python:true"},
					{"os:osx", "language:python"},
					{"os:osx", "language_python:true"},
					{"language:python"},
					{"language_python:true"},
					{"osx_image:xcode7"},
					{"os:osx"},
				},
				{
					{"language:objective-c", "language_objective-c:true", "osx_image:xcode6.4", "os:osx"},
					{"osx_image:xcode6.4", "language:objective-c"},
					{"osx_image:xcode6.4", "language_objective-c:true"},
					{"os:osx", "language:objective-c"},
					{"os:osx", "language_objective-c:true"},
					{"language:objective-c"},
					{"language_objective-c:true"},
					{"osx_image:xcode6.4"},
					{"os:osx"},
				},
				{
					{"language:node_js", "language_node_js:true", "osx_image:xcode6.1", "dist:yosammity", "group:fancy", "os:osx"},
					{"osx_image:xcode6.1", "language:node_js"},
					{"osx_image:xcode6.1", "language_node_js:true"},
					{"dist:yosammity", "language:node_js"},
					{"dist:yosammity", "language_node_js:true"},
					{"group:fancy", "language:node_js"},
					{"group:fancy", "language_node_js:true"},
					{"os:osx", "language:node_js"},
					{"os:osx", "language_node_js:true"},
					{"language:node_js"},
					{"language_node_js:true"},
					{"osx_image:xcode6.1"},
					{"dist:yosammity"},
					{"group:fancy"},
					{"os:osx"},
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
