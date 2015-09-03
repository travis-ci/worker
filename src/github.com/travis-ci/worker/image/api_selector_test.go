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
				&Params{
					Infra:    "gce",
					Language: "ruby",
					OS:       "linux",
				},
				&Params{
					Infra:    "gce",
					Language: "go",
					OS:       "linux",
					Group:    "dev",
				},
				&Params{
					Infra:    "macstadium6",
					Language: "python",
					OS:       "osx",
					OsxImage: "xcode7",
				},
				&Params{
					Infra:    "macstadium6",
					Language: "objective-c",
					OS:       "osx",
					OsxImage: "xcode6.4",
				},
				&Params{
					Infra:    "macstadium6",
					Language: "node_js",
					OsxImage: "xcode6.1",
					Dist:     "yosammity",
					Group:    "fancy",
					OS:       "osx",
				},
			},
			E: [][][]string{
				[][]string{
					[]string{"language:ruby", "language_ruby:true", "os:linux"},
					[]string{"os:linux", "language:ruby"},
					[]string{"os:linux", "language_ruby:true"},
					[]string{"language:ruby"},
					[]string{"language_ruby:true"},
					[]string{"os:linux"},
				},
				[][]string{
					[]string{"language:go", "language_go:true", "group:dev", "os:linux"},
					[]string{"group:dev", "language:go"},
					[]string{"group:dev", "language_go:true"},
					[]string{"os:linux", "language:go"},
					[]string{"os:linux", "language_go:true"},
					[]string{"language:go"},
					[]string{"language_go:true"},
					[]string{"group:dev"},
					[]string{"os:linux"},
				},
				[][]string{
					[]string{"language:python", "language_python:true", "osx_image:xcode7", "os:osx"},
					[]string{"osx_image:xcode7", "language:python"},
					[]string{"osx_image:xcode7", "language_python:true"},
					[]string{"os:osx", "language:python"},
					[]string{"os:osx", "language_python:true"},
					[]string{"language:python"},
					[]string{"language_python:true"},
					[]string{"osx_image:xcode7"},
					[]string{"os:osx"},
				},
				[][]string{
					[]string{"language:objective-c", "language_objective-c:true", "osx_image:xcode6.4", "os:osx"},
					[]string{"osx_image:xcode6.4", "language:objective-c"},
					[]string{"osx_image:xcode6.4", "language_objective-c:true"},
					[]string{"os:osx", "language:objective-c"},
					[]string{"os:osx", "language_objective-c:true"},
					[]string{"language:objective-c"},
					[]string{"language_objective-c:true"},
					[]string{"osx_image:xcode6.4"},
					[]string{"os:osx"},
				},
				[][]string{
					[]string{"language:node_js", "language_node_js:true", "osx_image:xcode6.1", "dist:yosammity", "group:fancy", "os:osx"},
					[]string{"osx_image:xcode6.1", "language:node_js"},
					[]string{"osx_image:xcode6.1", "language_node_js:true"},
					[]string{"dist:yosammity", "language:node_js"},
					[]string{"dist:yosammity", "language_node_js:true"},
					[]string{"group:fancy", "language:node_js"},
					[]string{"group:fancy", "language_node_js:true"},
					[]string{"os:osx", "language:node_js"},
					[]string{"os:osx", "language_node_js:true"},
					[]string{"language:node_js"},
					[]string{"language_node_js:true"},
					[]string{"osx_image:xcode6.1"},
					[]string{"dist:yosammity"},
					[]string{"group:fancy"},
					[]string{"os:osx"},
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
