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
	"github.com/cenk/backoff"
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
	httpClient  *http.Client
	httpBackOff *backoff.ExponentialBackOff
	baseURL     string
	authToken   string

	partsBuffer      []*httpLogPart
	partsBufferMutex *sync.Mutex

	maxBufferSize uint64
}

func newHTTPLogPartSink(ctx gocontext.Context, url, authToken string, maxBufferSize uint64) *httpLogPartSink {
	httpBackOff := backoff.NewExponentialBackOff()
	// TODO: make this configurable?
	httpBackOff.MaxInterval = 10 * time.Second
	httpBackOff.MaxElapsedTime = 3 * time.Minute

	lps := &httpLogPartSink{
		httpClient:       &http.Client{},
		httpBackOff:      httpBackOff,
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
		// NOTE: This error may deserve special handling at a higher level to do
		// something like canceling and resetting the job.  The implementation here
		// will result in a sentry error being captured (in http_log_writer)
		// assuming the necessary config bits are set.
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
		// NOTE: This is the point of origin for log parts backpressure, in
		// combination with the error returned by `.Add` when maxBufferSize is
		// reached.  Because running jobs will not be able to send their log parts
		// anywhere, it remains to be determined whether we should cancel (and
		// reset) running jobs or allow them to complete without capturing output.
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

	var resp *http.Response
	err = backoff.Retry(func() (err error) {
		resp, err = lps.httpClient.Do(req)
		if resp != nil && resp.StatusCode != http.StatusNoContent {
			return errors.Errorf("expected %d but got %d", http.StatusNoContent, resp.StatusCode)
		}
		return
	}, lps.httpBackOff)

	if err != nil {
		return errors.Wrap(err, "failed to send log parts with retries")
	}

	return nil
}
