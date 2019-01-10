package backend

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/travis-ci/worker/config"
)

type gceTestResponse struct {
	Status  int
	Headers map[string]string
	Body    string
}

type gceTestServer struct {
	Responses *gceTestResponseMap
}

type gceTestResponseMap struct {
	Map map[string]*gceTestResponse
}

func (s *gceTestServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	origURL := q.Get("_orig_req_url")

	resp, ok := s.Responses.Map[origURL]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "OOPS NOPE! %v", origURL)
		return
	}

	for key, value := range resp.Headers {
		w.Header().Set(key, value)
	}

	w.WriteHeader(resp.Status)
	io.WriteString(w, resp.Body)
}

func gceTestSetupGCEServer(resp *gceTestResponseMap) *httptest.Server {
	if resp == nil {
		resp = &gceTestResponseMap{Map: map[string]*gceTestResponse{}}
	}

	return httptest.NewServer(&gceTestServer{Responses: resp})
}

type gceTestRequestLog struct {
	Reqs []*http.Request
}

func (rl *gceTestRequestLog) Add(req *http.Request) {
	if rl.Reqs == nil {
		rl.Reqs = []*http.Request{}
	}

	rl.Reqs = append(rl.Reqs, req)
}

func gceTestSetup(t *testing.T, cfg *config.ProviderConfig, resp *gceTestResponseMap) (*gceProvider, *http.Transport, *gceTestRequestLog) {
	if cfg == nil {
		cfg = config.ProviderConfigFromMap(map[string]string{
			"ACCOUNT_JSON":    "{}",
			"PROJECT_ID":      "project_id",
			"IMAGE_ALIASES":   "foo",
			"IMAGE_ALIAS_FOO": "default",
		})
	}

	server := gceTestSetupGCEServer(resp)
	reqs := &gceTestRequestLog{}

	gceCustomHTTPTransportLock.Lock()
	transport := &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			reqs.Add(req)

			u, err := url.Parse(server.URL)
			if err != nil {
				return nil, err
			}
			q := u.Query()
			q.Set("_orig_req_url", req.URL.String())
			u.RawQuery = q.Encode()
			return u, nil
		},
	}
	gceCustomHTTPTransport = transport

	p, err := newGCEProvider(cfg)

	gceCustomHTTPTransport = nil
	gceCustomHTTPTransportLock.Unlock()

	if err != nil {
		t.Fatal(err)
	}

	provider := p.(*gceProvider)
	return provider, transport, reqs
}

func gceTestTeardown(p *gceProvider) {
	if p.cfg.IsSet("TEMP_DIR") {
		_ = os.RemoveAll(p.cfg.Get("TEMP_DIR"))
	}
}

func TestNewGCEProvider(t *testing.T) {
	p, _, _ := gceTestSetup(t, nil, nil)
	defer gceTestTeardown(p)
}

func TestGCEProvider_SetupMakesRequests(t *testing.T) {
	p, _, rl := gceTestSetup(t, nil, nil)
	err := p.Setup(context.TODO())

	assert.NotNil(t, err)
	assert.True(t, len(rl.Reqs) >= 1)
}
