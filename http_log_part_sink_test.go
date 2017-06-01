package worker

import (
	"testing"

	gocontext "context"

	"github.com/stretchr/testify/assert"
)

func TestNewHTTPLogPartSink(t *testing.T) {
	ctx, cancel := gocontext.WithCancel(gocontext.TODO())
	cancel()
	lps := newHTTPLogPartSink(
		ctx,
		"http://example.org/log-parts/multi",
		uint64(1000))

	assert.NotNil(t, lps)
}

func TestHTTPLogPartSink_flush(t *testing.T) {
	t.SkipNow()
	ctx := gocontext.TODO()
	lps := newHTTPLogPartSink(ctx, testHTTPLogSinkServer.URL, uint64(10))
	lps.flush(gocontext.TODO())
	lps.Add(ctx, &httpLogPart{
		JobID:   uint64(4),
		Content: "wat",
		Number:  3,
		Final:   false,
	})

	lps.partsBufferMutex.Lock()
	assert.Len(t, lps.partsBuffer, 1)
	lps.partsBufferMutex.Unlock()

	lps.flush(ctx)

	lps.partsBufferMutex.Lock()
	assert.Len(t, lps.partsBuffer, 0)
	lps.partsBufferMutex.Unlock()
}
