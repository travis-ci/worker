package worker

import (
	"fmt"
	"io"
)

func writeFold(w io.Writer, name string, b []byte) (int, error) {
	folded := []byte(fmt.Sprintf("travis_fold:start:%s\r\033[0K\n", name))
	folded = append(folded, b...)

	if string(folded[len(folded)-1]) != "\n" {
		folded = append(folded, []byte("\n")...)
	}

	folded = append(folded, []byte(fmt.Sprintf("travis_fold:end:%s\r\033[0K\n", name))...)
	return w.Write(folded)
}
