package backend

import (
	gocontext "context"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/travis-ci/worker/context"
)

func Test_asBool(t *testing.T) {
	for s, b := range map[string]bool{
		"yes":     true,
		"on":      true,
		"1":       true,
		"boo":     true,
		"0":       false,
		"99":      true,
		"a":       true,
		"off":     false,
		"no":      false,
		"fafafaf": true,
		"true":    true,
		"false":   false,
		"":        false,
	} {
		assert.Equal(t, b, asBool(s))
	}
}

func Test_hostnameFromContext(t *testing.T) {
	jobID := rand.Uint64()

	for _, tc := range []struct{ r, n string }{
		{
			r: "friendly/fribble",
			n: fmt.Sprintf("travis-job-friendly-fribble-%v", jobID),
		},
		{
			r: "very-SiLlY.nAmE.wat/por-cu___-pine",
			n: fmt.Sprintf("travis-job-very-silly-nam-por-cu-pine-%v", jobID),
		},
	} {
		ctx := context.FromRepository(context.FromJobID(gocontext.TODO(), jobID), tc.r)
		assert.Equal(t, tc.n, hostnameFromContext(ctx))
	}

	randName := hostnameFromContext(gocontext.TODO())
	randParts := strings.Split(randName, "-")
	assert.Len(t, randParts, 9)
	assert.Equal(t, "unk", randParts[2])
	assert.Equal(t, "unk", randParts[3])
}

func Test_str2Map(t *testing.T) {
	s := "foo:bar,bang:baz Hello:World, extra space:justBecause sillychars:butwhy%3F encodedspace:yup+, colonInside:why%3Anot"
	m := str2map(s, " ,")
	e := map[string]string{
		"foo":          "bar",
		"bang":         "baz",
		"Hello":        "World",
		"extra":        "",
		"space":        "justBecause",
		"sillychars":   "butwhy?",
		"encodedspace": "yup ",
		"colonInside":  "why:not",
	}
	assert.Equal(t, e, m)
}
