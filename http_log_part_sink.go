package worker

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	gocontext "context"

	"github.com/Sirupsen/logrus"
	"github.com/pkg/errors"
	"github.com/travis-ci/worker/context"
)

var (
	defaultHTTPLogPartSink      *httpLogPartSink
	defaultHTTPLogPartSinkMutex = &sync.Mutex{}

	maxHTTPLogPartSinkBufferSizeErr = fmt.Errorf("log sink buffer size maximum reached")
)

const (
	// defaultHTTPLogPartSinkMaxBufferSize of 150 should be roughly 250MB of
	// buffer on the high end of possible LogChunkSize, which is somewhat
	// arbitrary, but is an amount of memory per worker process that we can
	// tolerate on all hosted infrastructures and should allow for enough wiggle
	// room that we don't hit log sink buffer backpressure unless something is
	// seriously broken with log parts publishing. ~@meatballhat
	defaultHTTPLogPartSinkMaxBufferSize = 150
)

type httpLogPartSink struct {
	httpClient *http.Client
	baseURL    string
	authToken  string

	partsBuffer      []*httpLogPart
	partsBufferMutex *sync.Mutex

	maxBufferSize uint64
}

func newHTTPLogPartSink(ctx gocontext.Context, url, authToken string, maxBufferSize uint64) *httpLogPartSink {
	lps := &httpLogPartSink{
		httpClient:       &http.Client{},
		baseURL:          url,
		authToken:        authToken,
		partsBuffer:      []*httpLogPart{},
		partsBufferMutex: &sync.Mutex{},
		maxBufferSize:    maxBufferSize,
	}

	go lps.flushRegularly(ctx)

	return lps
}

func (lps *httpLogPartSink) Add(part *httpLogPart) error {
	if len(lps.partsBuffer) >= int(lps.maxBufferSize) {
		return maxHTTPLogPartSinkBufferSizeErr
	}

	lps.partsBufferMutex.Lock()
	defer lps.partsBufferMutex.Unlock()

	lps.partsBuffer = append(lps.partsBuffer, part)
	return nil
}

func (lps *httpLogPartSink) flushRegularly(ctx gocontext.Context) {
	ticker := time.NewTicker(LogWriterTick)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			lps.flush(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (lps *httpLogPartSink) flush(ctx gocontext.Context) {
	lps.partsBufferMutex.Lock()
	defer lps.partsBufferMutex.Unlock()

	payload := []*httpLogPartPayload{}

	for _, part := range lps.partsBuffer {
		payload = append(payload, &httpLogPartPayload{
			Type:     "log_part",
			JobID:    part.JobID,
			Final:    part.Final,
			Content:  base64.StdEncoding.EncodeToString([]byte(part.Content)),
			Encoding: "base64",
		})
	}

	err := lps.publishLogParts(payload)
	if err != nil {
		context.LoggerFromContext(ctx).WithFields(logrus.Fields{
			"self": "http_log_part_sink",
			"err":  err,
		}).Error("failed to publish buffered parts")
		return
	}

	lps.partsBuffer = []*httpLogPart{}
}

func (lps *httpLogPartSink) publishLogParts(payload []*httpLogPartPayload) error {
	publishURL, err := url.Parse(lps.baseURL)
	if err != nil {
		return errors.Wrap(err, "couldn't parse base URL")
	}
	publishURL.Path = "/log-parts/multi"

	payloadBody, err := json.Marshal(payload)
	if err != nil {
		return errors.Wrap(err, "couldn't marshal JSON")
	}

	req, err := http.NewRequest("POST", publishURL.String(), bytes.NewReader(payloadBody))
	if err != nil {
		return errors.Wrap(err, "couldn't create request")
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", lps.authToken))

	resp, err := lps.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "error making request")
	}

	if resp.StatusCode != http.StatusNoContent {
		return errors.Errorf("expected %d but got %d", http.StatusNoContent, resp.StatusCode)
	}

	return nil
}
