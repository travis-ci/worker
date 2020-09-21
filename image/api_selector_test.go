package image

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

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
					Infra:    "jupiterbrain",
					Language: "python",
					OS:       "osx",
					OsxImage: "xcode7",
					JobID:    uint64(4),
					Repo:     "corp/frob",
				},
				{
					Infra:    "jupiterbrain",
					Language: "objective-c",
					OS:       "osx",
					OsxImage: "xcode6.4",
					JobID:    uint64(4),
					Repo:     "corp/frob",
				},
				{
					Infra:    "jupiterbrain",
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
					&tagSet{[]string{"group_dev:true", "language_go:true", "os:linux"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"group_dev:true", "language_go:true"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_go:true", "os:linux"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_go:true"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"group_dev:true"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"os:linux"}, true, uint64(4), "corp/frob"},
				},
				{
					&tagSet{[]string{"dist:precise", "group_edge:true", "language_ruby:true", "os:linux"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"dist:precise", "group_edge:true", "language_ruby:true"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"dist:precise", "language_ruby:true"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"group_edge:true", "language_ruby:true"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_ruby:true", "os:linux"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"dist:precise"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_ruby:true"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"dist:precise"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"group_edge:true"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"os:linux"}, true, uint64(4), "corp/frob"},
				},
				{
					&tagSet{[]string{"language_python:true", "os:osx", "osx_image:xcode7"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"os:osx", "osx_image:xcode7"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_python:true", "os:osx"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_python:true"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"osx_image:xcode7"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"os:osx"}, true, uint64(4), "corp/frob"},
				},
				{
					&tagSet{[]string{"language_objective-c:true", "os:osx", "osx_image:xcode6.4"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"os:osx", "osx_image:xcode6.4"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_objective-c:true", "os:osx"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_objective-c:true"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"osx_image:xcode6.4"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"os:osx"}, true, uint64(4), "corp/frob"},
				},
				{
					&tagSet{[]string{"dist:yosammity", "group_fancy:true", "language_node_js:true", "os:osx", "osx_image:xcode6.1"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"os:osx", "osx_image:xcode6.1"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"dist:yosammity", "group_fancy:true", "language_node_js:true"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"dist:yosammity", "language_node_js:true"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"group_fancy:true", "language_node_js:true"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_node_js:true", "os:osx"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"dist:yosammity"}, false, uint64(4), "corp/frob"},
					&tagSet{[]string{"language_node_js:true"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"osx_image:xcode6.1"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"dist:yosammity"}, true, uint64(4), "corp/frob"},
					&tagSet{[]string{"group_fancy:true"}, true, uint64(4), "corp/frob"},
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
		fmt.Fprint(w, testAPIServerString)
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)

	as := NewAPISelector(u)

	actual, _ := as.Select(context.TODO(), &Params{
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
		fmt.Fprint(w, testAPIServerEmptyResponseString)
	}))
	defer ts.Close()
	u, _ := url.Parse(ts.URL)
	actual, err := NewAPISelector(u).Select(context.TODO(), &Params{})
	assert.Equal(t, actual, "default")
	assert.NoError(t, err)
}

func TestAPISelector_SelectDefaultWhenBadResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	u, _ := url.Parse(ts.URL)
	as := NewAPISelector(u)
	as.SetMaxInterval(time.Millisecond)
	as.SetMaxElapsedTime(10 * time.Millisecond)
	actual, err := as.Select(context.TODO(), &Params{})
	assert.Equal(t, actual, "default")
	assert.EqualError(t, err, "expected 200 status code from job-board, received status=500 body=\"\"")
}

func TestAPISelector_SelectDefaultWhenBadJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, `{"data`)
	}))
	defer ts.Close()
	u, _ := url.Parse(ts.URL)
	actual, err := NewAPISelector(u).Select(context.TODO(), &Params{})
	assert.Equal(t, actual, "default")
	assert.EqualError(t, err, "unexpected end of JSON input")
}

func TestAPISelector_SelectTrailingComma(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprint(w, testAPIServerString)
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)

	as := NewAPISelector(u)

	actual, err := as.Select(context.TODO(), &Params{
		Infra:    "test,",
		Language: "ruby,",
		OsxImage: "meow,",
		Dist:     "yosamitty,",
		Group:    "dev,",
		OS:       "osx,",
	})
	assert.Equal(t, actual, "default")
	assert.EqualError(t, err, "job was aborted because tag \"dist:yosamitty,\" contained \",\", this can happen when .travis.yml has a trailing comma")
}

func TestAPISelector_buildCandidateTags(t *testing.T) {
	as := NewAPISelector(nil)

	for _, tc := range testAPITagTestCases {
		for i, params := range tc.P {
			expectedJSON, _ := json.MarshalIndent(tc.E[i], "", "  ")
			tagSets, _ := as.buildCandidateTags(params)
			actualJSON, _ := json.MarshalIndent(tagSets, "", "  ")
			assert.JSONEq(t, string(expectedJSON), string(actualJSON))
		}
	}
}
