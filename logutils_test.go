package main

import (
	"bytes"
	"strings"
	"testing"
)

func assertContains(t *testing.T, str, substr string) {
	if !strings.Contains(str, substr) {
		t.Errorf("expected %q to contain %q, but didn't", str, substr)
	}
}

func TestLogger_Set(t *testing.T) {
	buf := new(bytes.Buffer)
	log := NewLogger(buf, "")

	err := log.Set("foo", "bar").Info("")
	if err != nil {
		t.Errorf("expected Info() not to return error, got %v", err)
	}

	assertContains(t, buf.String(), "foo=bar")
}

func TestLogger_Info(t *testing.T) {
	buf := new(bytes.Buffer)
	log := NewLogger(buf, "")

	err := log.Info("hello world")
	if err != nil {
		t.Errorf("expected Info() not to return error, got %v", err)
	}

	assertContains(t, buf.String(), "level=info")
	assertContains(t, buf.String(), `message="hello world"`)
}

func TestLogger_Infof(t *testing.T) {
	buf := new(bytes.Buffer)
	log := NewLogger(buf, "")

	err := log.Infof("hello %d", 1)
	if err != nil {
		t.Errorf("expected Info() not to return error, got %v", err)
	}

	assertContains(t, buf.String(), "level=info")
	assertContains(t, buf.String(), `message="hello 1"`)
}

func TestLogger_Warn(t *testing.T) {
	buf := new(bytes.Buffer)
	log := NewLogger(buf, "")

	err := log.Warn("hello world")
	if err != nil {
		t.Errorf("expected Info() not to return error, got %v", err)
	}

	assertContains(t, buf.String(), "level=warn")
	assertContains(t, buf.String(), `message="hello world"`)
}

func TestLogger_Warnf(t *testing.T) {
	buf := new(bytes.Buffer)
	log := NewLogger(buf, "")

	err := log.Warnf("hello %d", 1)
	if err != nil {
		t.Errorf("expected Info() not to return error, got %v", err)
	}

	assertContains(t, buf.String(), "level=warn")
	assertContains(t, buf.String(), `message="hello 1"`)
}

func TestLogger_Error(t *testing.T) {
	buf := new(bytes.Buffer)
	log := NewLogger(buf, "")

	err := log.Error("hello world")
	if err != nil {
		t.Errorf("expected Info() not to return error, got %v", err)
	}

	assertContains(t, buf.String(), "level=error")
	assertContains(t, buf.String(), `message="hello world"`)
}

func TestLogger_Errorf(t *testing.T) {
	buf := new(bytes.Buffer)
	log := NewLogger(buf, "")

	err := log.Errorf("hello %d", 1)
	if err != nil {
		t.Errorf("expected Info() not to return error, got %v", err)
	}

	assertContains(t, buf.String(), "level=error")
	assertContains(t, buf.String(), `message="hello 1"`)
}
