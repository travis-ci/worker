package worker

import (
	"time"

	"github.com/Sirupsen/logrus"
)

func init() {
	logrus.SetLevel(logrus.FatalLevel)
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
