package backend

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type recordingHTTPTransport struct {
	req *http.Request
}

func (t *recordingHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.req = req
	return nil, fmt.Errorf("recording HTTP transport impl")
}

type testConfigGetter struct {
	m map[string]interface{}
}

func (c *testConfigGetter) String(k string) string {
	if v, ok := c.m[k]; ok {
		return v.(string)
	}
	return ""
}

func (c *testConfigGetter) StringSlice(k string) []string {
	if v, ok := c.m[k]; ok {
		return v.([]string)
	}
	return []string{}
}

func (c *testConfigGetter) Bool(k string) bool {
	if v, ok := c.m[k]; ok {
		return v.(bool)
	}
	return false
}

func (c *testConfigGetter) Int(k string) int {
	if v, ok := c.m[k]; ok {
		return v.(int)
	}
	return 0
}

func (c *testConfigGetter) Duration(k string) time.Duration {
	if v, ok := c.m[k]; ok {
		return v.(time.Duration)
	}
	return 0 * time.Second
}

func (c *testConfigGetter) Set(k string, v interface{}) {
	c.m[k] = v
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
