package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"time"

	"github.com/rcrowley/go-metrics"
	"golang.org/x/net/context"
)

// A BuildScriptGeneratorError is sometimes used by the Generate method on a
// BuildScriptGenerator to return more metadata about an error.
type BuildScriptGeneratorError struct {
	error

	// true when this error can be recovered by retrying later
	Recover bool
}

// A BuildScriptGenerator generates a build script for a given job payload.
type BuildScriptGenerator interface {
	Generate(context.Context, JobPayload) ([]byte, error)
}

type webBuildScriptGenerator struct {
	URL string
}

// NewBuildScriptGenerator creates a generator backed by an HTTP API.
func NewBuildScriptGenerator(URL string) BuildScriptGenerator {
	return &webBuildScriptGenerator{URL: URL}
}

func (g *webBuildScriptGenerator) Generate(ctx context.Context, payload JobPayload) ([]byte, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	var token string
	u, err := url.Parse(g.URL)
	if err != nil {
		return nil, err
	}
	if u.User != nil {
		token = u.User.Username()
		u.User = nil
	}

	buf := bytes.NewBuffer(b)
	req, err := http.NewRequest("POST", u.String(), buf)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("worker-go v=%v rev=%v d=%v", VersionString, RevisionString, GeneratedString))
	req.Header.Set("Content-Type", "application/json")

	startRequest := time.Now()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	metrics.GetOrRegisterTimer("worker.job.script.api", metrics.DefaultRegistry).UpdateSince(startRequest)

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 500 {
		return nil, BuildScriptGeneratorError{error: fmt.Errorf("server error: %q", string(body)), Recover: true}
	} else if resp.StatusCode >= 400 {
		return nil, BuildScriptGeneratorError{error: fmt.Errorf("client error: %q", string(body)), Recover: false}
	}

	return body, nil
}
