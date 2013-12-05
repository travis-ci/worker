package main

import (
	"encoding/json"
	"testing"
)

func TestReporter_Write(t *testing.T) {
	mb := NewTestMessageBroker()
	mb.DeclareQueue("reporting.jobs.logs")

	r, _ := NewReporter(mb, 1)
	r.Write([]byte("foo\x00bar"))

	var lp logPart
	json.Unmarshal(<-mb.(*TestMessageBroker).queues["reporting.jobs.logs"], &lp)

	if lp.Log != "foobar" {
		t.Error("NUL byte was not stripped")
	}
}
