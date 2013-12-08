package main

import (
	"bytes"
	"testing"
)

func TestLogger(t *testing.T) {
	buf := new(bytes.Buffer)
	log := NewLogger(buf)

	err := log.Set("foo", "bar").Info("hello world")
	if err != nil {
		t.Errorf("expected Info() not to return error, got %v", err)
	}

	expected := `foo=bar level=info source=logutils_test message="hello world"`
	if buf.String() != expected {
		t.Errorf("expected log to be '%v' but was '%v'", expected, buf.String())
	}
}
