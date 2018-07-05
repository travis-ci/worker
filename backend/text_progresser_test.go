package backend

import (
	"bytes"
	"io/ioutil"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewTextProgresser(t *testing.T) {
	w := &bytes.Buffer{}
	tp := NewTextProgresser(w)

	assert.NotNil(t, tp)
	assert.NotNil(t, tp.w)

	tp = NewTextProgresser(nil)
	assert.NotNil(t, tp)
	assert.NotNil(t, tp.w)
	assert.Equal(t, tp.w, ioutil.Discard)
}

var testTextProgresserProgressCases = []struct {
	e *ProgressEntry
	o string
}{
	{e: &ProgressEntry{Message: "hello"},
		o: "• hello\r\n"},
	{e: &ProgressEntry{Message: "yay", State: ProgressSuccess},
		o: "✓ yay\r\n"},
	{e: &ProgressEntry{Message: "oh no", State: ProgressFailure},
		o: "✗ oh no\r\n"},
	{e: &ProgressEntry{Message: "hello...", Continues: true},
		o: "• hello..."},
	{e: &ProgressEntry{Message: "yay...", State: ProgressSuccess, Continues: true},
		o: "✓ yay..."},
	{e: &ProgressEntry{Message: "oh no...", State: ProgressFailure, Continues: true},
		o: "✗ oh no..."},
	{e: &ProgressEntry{Message: "hello!", Interrupts: true},
		o: "\r\n• hello!\r\n"},
	{e: &ProgressEntry{Message: "yay!", State: ProgressSuccess, Interrupts: true},
		o: "\r\n✓ yay!\r\n"},
	{e: &ProgressEntry{Message: "oh no!", State: ProgressFailure, Interrupts: true},
		o: "\r\n✗ oh no!\r\n"},
	{e: &ProgressEntry{Message: "hello!...", Interrupts: true, Continues: true},
		o: "\r\n• hello!..."},
	{e: &ProgressEntry{Message: "yay!...", State: ProgressSuccess, Interrupts: true, Continues: true},
		o: "\r\n✓ yay!..."},
	{e: &ProgressEntry{Message: "oh no!...", State: ProgressFailure, Interrupts: true, Continues: true},
		o: "\r\n✗ oh no!..."},
	{e: &ProgressEntry{Message: "hello", Raw: true},
		o: "hello"},
	{e: &ProgressEntry{Message: "yay", State: ProgressSuccess, Raw: true},
		o: "yay"},
	{e: &ProgressEntry{Message: "oh no", State: ProgressFailure, Raw: true},
		o: "oh no"},
}

func TestTextProgresser_Progress(t *testing.T) {
	w := &bytes.Buffer{}
	tp := NewTextProgresser(w)

	for _, entry := range testTextProgresserProgressCases {
		buf := &bytes.Buffer{}
		tp.w = buf
		tp.Progress(entry.e)
		assert.Equal(t, entry.o, buf.String())
	}
}
