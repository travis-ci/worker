package lib

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

func (t *testWriter) String() string {
	return string(bytes.Join(t.written, []byte{}))
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

func TestCoalesceWriteCloser_Write(t *testing.T) {
	tw := &testWriter{written: make([][]byte, 0)}
	cw := NewCoalesceWriteCloser(tw)

	fmt.Fprint(cw, "hello ")
	fmt.Fprint(cw, "world")
	cw.Close()

	if !bytes.Equal(tw.written[0], []byte("hello world")) && len(tw.written) != 1 {
		t.Errorf("expected writes to be coalesced, got %s", bytes.Join(tw.written, []byte(", ")))
	}
}

func TestCoalesceWriteCloser_Close(t *testing.T) {
	tw := &testWriter{written: make([][]byte, 0)}
	cw := NewCoalesceWriteCloser(tw)
	cw.Close()

	if !tw.closed {
		t.Error("expected Close() to propagate")
	}
}

func TestTimeoutWriter_Timeout(t *testing.T) {
	testWriter := &testWriter{written: make([][]byte, 0)}
	tw := NewTimeoutWriter(testWriter, 2*time.Millisecond)
	fmt.Fprint(tw, "hello world")
	time.Sleep(5 * time.Millisecond)

	select {
	case <-tw.Timeout:
		// pass
	default:
		t.Errorf("expected TimeoutWriter to send a timeout")
	}
}

func TestTimeoutWriter_Write(t *testing.T) {
	testWriter := &testWriter{written: make([][]byte, 0)}
	tw := NewTimeoutWriter(testWriter, 2*time.Millisecond)
	defer tw.Close()

	fmt.Fprint(tw, "hello world")

	if testWriter.String() != "hello world" {
		t.Errorf("expected written string to propagage, %q was written", testWriter.String())
	}
}

func TestTimeoutWriter_Close(t *testing.T) {
	testWriter := &testWriter{written: make([][]byte, 0)}
	tw := NewTimeoutWriter(testWriter, 2*time.Millisecond)
	tw.Close()

	if !testWriter.closed {
		t.Error("expected Close() to propagate")
	}
}

func TestLimitWriter_LimitReached(t *testing.T) {
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

func TestLimitWriter_Write(t *testing.T) {
	tw := &testWriter{written: make([][]byte, 0)}
	lw := NewLimitWriter(tw, 10)
	defer lw.Close()

	fmt.Fprint(lw, "hello world")

	if tw.String() != "hello world" {
		t.Errorf("expected written string to propagage, %q was written", tw.String())
	}
}

func TestLimitWriter_Close(t *testing.T) {
	tw := &testWriter{written: make([][]byte, 0)}
	lw := NewLimitWriter(tw, 10)
	lw.Close()

	if !tw.closed {
		t.Error("expected Close() to propagate")
	}
}
