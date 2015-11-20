package backend

import (
	"fmt"
	"net/http"
)

type recordingHTTPTransport struct {
	req *http.Request
}

func (t *recordingHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.req = req
	return nil, fmt.Errorf("recording HTTP transport impl")
}
