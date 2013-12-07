package main

import (
	"bytes"
	"fmt"
	"testing"
	"time"
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

func TestTimeoutWriter(t *testing.T) {
	testWriter := &testWriter{written: make([][]byte, 0)}
	tw := NewTimeoutWriter(testWriter, 2*time.Millisecond)
	fmt.Fprint(tw, "hello world")
	timedout := new(bool)
	go func() {
		<-tw.Timeout
		*timedout = true
	}()
	time.Sleep(5 * time.Millisecond)

	if !*timedout {
		t.Errorf("expected TimeoutWriter to send a timeout")
	}
}

func TestLimitWriter(t *testing.T) {
	tw := &testWriter{written: make([][]byte, 0)}
	lw := NewLimitWriter(tw, 10)
	fmt.Fprintf(lw, "0123456789")

	limitReached := false
	select {
	case <-lw.LimitReached:
		limitReached = true
	default:
	}

	if limitReached {
		t.Error("expected limit to not have been reached after writing n bytes")
	}

	fmt.Fprintf(lw, "a")

	select {
	case <-lw.LimitReached:
		limitReached = true
	default:
	}

	if !limitReached {
		t.Error("expected limit to have been reached after writing n+1 bytes")
	}
}
