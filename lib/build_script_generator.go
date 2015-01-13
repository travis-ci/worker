package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

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

	buf := bytes.NewBuffer(b)
	resp, err := http.Post(g.URL, "application/json", buf)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 500 {
		return nil, BuildScriptGeneratorError{error: fmt.Errorf("server error: %v", body), Recover: true}
	} else if resp.StatusCode >= 400 {
		return nil, BuildScriptGeneratorError{error: fmt.Errorf("client error: %v", body), Recover: false}
	}

	return body, nil
}
