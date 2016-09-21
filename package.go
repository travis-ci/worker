package worker

import (
	"fmt"
	"io"
	"strings"
)

func writeFold(w io.Writer, name string, b []byte) (int, error) {
	folded := []byte(fmt.Sprintf("travis_fold:start:%s\r\033[0K", name))
	folded = append(folded, b...)

	if string(folded[len(folded)-1]) != "\n" {
		folded = append(folded, []byte("\n")...)
	}

	folded = append(folded, []byte(fmt.Sprintf("travis_fold:end:%s\r\033[0K", name))...)
	return w.Write(folded)
}

func stringSplitSpace(s string) []string {
	parts := []string{}
	for _, part := range strings.Split(s, " ") {
		parts = append(parts, strings.TrimSpace(part))
	}
	return parts
}
