package worker

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitly/go-simplejson"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
)

func TestBuildScriptGenerator(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello, client")
	}))
	defer ts.Close()

	gen := NewBuildScriptGenerator(ts.URL)

	payload := simplejson.New()

	script, err := gen.Generate(context.TODO(), payload)
	require.Nil(t, err)
	require.Equal(t, []byte("Hello, client\n"), script)
}
