package backend

import (
	"io"
	"io/ioutil"
)

var textProgressHeads = map[ProgressState]string{
	ProgressSuccess: "✓ ",
	ProgressFailure: "✗ ",
	ProgressNeutral: "• ",
}

type TextProgresser struct {
	w io.Writer
}

func NewTextProgresser(w io.Writer) *TextProgresser {
	if w == nil {
		w = ioutil.Discard
	}
	return &TextProgresser{w: w}
}

func (tp *TextProgresser) Progress(entry *ProgressEntry) {
	head := textProgressHeads[entry.State]
	tail := "\r\n"

	if entry.Interrupts {
		head = "\r\n" + head
	}

	if entry.Continues {
		tail = ""
	}

	if entry.Raw {
		head = ""
		tail = ""
	}

	_, _ = tp.w.Write([]byte(head + entry.Message + tail))
}
