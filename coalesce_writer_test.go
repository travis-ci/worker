package main

import (
	"bytes"
	"fmt"
	"testing"
)

type testWriter struct {
	written [][]byte
	closed  bool
}

func (t *testWriter) Write(p []byte) (int, error) {
	b := make([]byte, len(p))
	copy(b, p)
	t.written = append(t.written, b)
	return len(p), nil
}

func (t *testWriter) Close() error {
	t.closed = true
	return nil
}

func TestCoalesceWriteCloser(t *testing.T) {
	tw := &testWriter{written: make([][]byte, 0)}
	cw := NewCoalesceWriteCloser(tw)

	fmt.Fprint(cw, "hello ")
	fmt.Fprint(cw, "world")
	cw.Close()

	if !bytes.Equal(tw.written[0], []byte("hello world")) && len(tw.written) != 1 {
		t.Errorf("expected writes to be coalesced, got %s", bytes.Join(tw.written, []byte(", ")))
	}

	if !tw.closed {
		t.Error("expected Close() to propagate")
	}
}
