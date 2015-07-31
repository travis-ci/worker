package context

import "testing"

func TestNothing(t *testing.T) {
	if 1 != 1 {
		t.Fail()
	}
}
