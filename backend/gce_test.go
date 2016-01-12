package backend

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

func gceTestSetup(t *testing.T, c *testConfigGetter, resp *gceTestResponseMap) (*gceProvider, *http.Transport, *gceTestRequestLog) {
	if c == nil {
		c = &testConfigGetter{
			m: map[string]interface{}{
				"account-json":        "{}",
				"project-id":          "project_id",
				"image-selector-type": "env",
				"image-aliases":       []string{"foo=default"},
				"rate-limit-tick":     1 * time.Second,
			},
		}
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

	p, err := newGCEProvider(c)

	gceCustomHTTPTransport = nil
	gceCustomHTTPTransportLock.Unlock()

	if err != nil {
		t.Fatal(err)
	}

	provider := p.(*gceProvider)
	return provider, transport, reqs
}

func gceTestTeardown(p *gceProvider) {
}

func TestNewGCEProvider(t *testing.T) {
	p, _, _ := gceTestSetup(t, nil, nil)
	defer gceTestTeardown(p)
}

func TestNewGCEProvider_RequiresProjectID(t *testing.T) {
	_, err := newGCEProvider(&testConfigGetter{
		m: map[string]interface{}{
			"account-json": "{}",
		},
	})

	if !assert.NotNil(t, err) {
		t.Fatal(fmt.Errorf("unexpected nil error"))
	}

	assert.Equal(t, err.Error(), "missing project-id")
}

func TestGCEProvider_SetupMakesRequests(t *testing.T) {
	p, _, rl := gceTestSetup(t, nil, nil)
	err := p.Setup()

	assert.NotNil(t, err)
	assert.Len(t, rl.Reqs, 1)
}
