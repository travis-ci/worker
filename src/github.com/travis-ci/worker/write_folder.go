package worker

import (
	"fmt"
	"io"
)

type writeFolder interface {
	WriteFold(string, []byte) (int, error)
}

func writeFold(w io.Writer, name string, b []byte) (int, error) {
	folded := []byte(fmt.Sprintf("travis_fold start %s\n", name))
	folded = append(folded, b...)

	if string(folded[len(folded)-1]) != "\n" {
		folded = append(folded, []byte("\n")...)
	}

	folded = append(folded, []byte(fmt.Sprintf("travis_fold end %s\n", name))...)
	return w.Write(folded)
}
