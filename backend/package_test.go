package backend

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

type recordingHTTPTransport struct {
	req *http.Request
}

func (t *recordingHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.req = req
	return nil, fmt.Errorf("recording HTTP transport impl")
}

func TestAsBool(t *testing.T) {
	for s, b := range map[string]bool{
		"yes":     true,
		"on":      true,
		"1":       true,
		"boo":     true,
		"0":       false,
		"99":      true,
		"a":       true,
		"off":     false,
		"no":      false,
		"fafafaf": true,
		"":        false,
	} {
		assert.Equal(t, b, asBool(s))
	}
}
