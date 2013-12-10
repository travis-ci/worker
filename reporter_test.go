package main

import (
	"encoding/json"
	"testing"
)

func TestLogWriter_Write(t *testing.T) {
	mb := NewTestMessageBroker()
	mb.DeclareQueue("reporting.jobs.logs")

	r := NewLogWriter(mb, 1)
	n, _ := r.Write([]byte("foo\x00bar"))

	if n != 7 {
		t.Errorf("Write() didn't write all bytes")
	}

	var lp logPart
	json.Unmarshal(<-mb.(*TestMessageBroker).queues["reporting.jobs.logs"], &lp)

	if lp.Log != "foobar" {
		t.Error("NUL byte was not stripped")
	}

	r.Write([]byte{})
	select {
	case <-mb.(*TestMessageBroker).queues["reporting.jobs.logs"]:
		t.Error("zero-length slice should not be written")
	default:
	}
}
