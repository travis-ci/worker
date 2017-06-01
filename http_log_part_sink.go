package worker

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	gocontext "context"

	"github.com/Sirupsen/logrus"
	"github.com/cenk/backoff"
	"github.com/pkg/errors"
	"github.com/travis-ci/worker/context"
)

var (
	httpLogPartSinksByURL      = map[string]*httpLogPartSink{}
	httpLogPartSinksByURLMutex = &sync.Mutex{}
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

type httpLogPartEncodedPayload struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
	Final    bool   `json:"final"`
	JobID    uint64 `json:"job_id"`
	Number   int    `json:"number"`
	Token    string `json:"tok"`
	Type     string `json:"@type"`
}

type httpLogPartSink struct {
	httpClient *http.Client
	baseURL    string

	partsBuffer      []*httpLogPart
	partsBufferMutex *sync.Mutex

	maxBufferSize uint64
}

func newHTTPLogPartSink(ctx gocontext.Context, url string, maxBufferSize uint64) *httpLogPartSink {
	lps := &httpLogPartSink{
		httpClient:       &http.Client{},
		baseURL:          url,
		partsBuffer:      []*httpLogPart{},
		partsBufferMutex: &sync.Mutex{},
		maxBufferSize:    maxBufferSize,
	}

	go lps.flushRegularly(ctx)

	return lps
}

func (lps *httpLogPartSink) Add(ctx gocontext.Context, part *httpLogPart) error {
	lps.partsBufferMutex.Lock()
	defer lps.partsBufferMutex.Unlock()

	if len(lps.partsBuffer) >= int(lps.maxBufferSize) {
		// NOTE: This error may deserve special handling at a higher level to do
		// something like canceling and resetting the job.  The implementation here
		// will result in a sentry error being captured (in http_log_writer)
		// assuming the necessary config bits are set.
		return fmt.Errorf("log sink buffer reached maximum size %d", lps.maxBufferSize)
	}

	lps.partsBuffer = append(lps.partsBuffer, part)

	context.LoggerFromContext(ctx).WithFields(logrus.Fields{
		"self": "http_log_part_sink",
		"size": len(lps.partsBuffer),
	}).Debug("appended to log parts buffer")

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
	logger := context.LoggerFromContext(ctx).WithField("self", "http_log_part_sink")

	lps.partsBufferMutex.Lock()
	logger.WithField("size", len(lps.partsBuffer)).Debug("flushing log parts buffer")
	bufferSample := make([]*httpLogPart, len(lps.partsBuffer))
	copy(bufferSample, lps.partsBuffer)
	lps.partsBuffer = []*httpLogPart{}
	lps.partsBufferMutex.Unlock()

	payload := []*httpLogPartEncodedPayload{}

	for _, part := range bufferSample {
		logger.WithFields(logrus.Fields{
			"job_id": part.JobID,
			"number": part.Number,
		}).Debug("appending encoded log part to payload")

		payload = append(payload, &httpLogPartEncodedPayload{
			Content:  base64.StdEncoding.EncodeToString([]byte(part.Content)),
			Encoding: "base64",
			Final:    part.Final,
			JobID:    part.JobID,
			Number:   part.Number,
			Token:    part.Token,
			Type:     "log_part",
		})
	}

	err := lps.publishLogParts(ctx, payload)
	if err != nil {
		// NOTE: This is the point of origin for log parts backpressure, in
		// combination with the error returned by `.Add` when maxBufferSize is
		// reached.  Because running jobs will not be able to send their log parts
		// anywhere, it remains to be determined whether we should cancel (and
		// reset) running jobs or allow them to complete without capturing output.
		for _, part := range bufferSample {
			addErr := lps.Add(ctx, part)
			if addErr != nil {
				logger.WithField("err", addErr).Error("failed to re-add buffer sample log part")
			}
		}
		logger.WithField("err", err).Error("failed to publish buffered parts")
	}
}

func (lps *httpLogPartSink) publishLogParts(ctx gocontext.Context, payload []*httpLogPartEncodedPayload) error {
	publishURL, err := url.Parse(lps.baseURL)
	if err != nil {
		return errors.Wrap(err, "couldn't parse base URL")
	}

	payloadBody, err := json.Marshal(payload)
	if err != nil {
		return errors.Wrap(err, "couldn't marshal JSON")
	}

	req, err := http.NewRequest("POST", publishURL.String(), bytes.NewReader(payloadBody))
	if err != nil {
		return errors.Wrap(err, "couldn't create request")
	}

	req.Header.Set("Authorization", fmt.Sprintf("token sig:%s", lps.generatePayloadSignature(payload)))
	req = req.WithContext(ctx)

	httpBackOff := backoff.NewExponentialBackOff()
	// TODO: make this configurable?
	httpBackOff.MaxInterval = 10 * time.Second
	httpBackOff.MaxElapsedTime = 3 * time.Minute

	var resp *http.Response
	err = backoff.Retry(func() (err error) {
		resp, err = lps.httpClient.Do(req)
		if resp != nil && resp.StatusCode != http.StatusNoContent {
			return errors.Errorf("expected %d but got %d", http.StatusNoContent, resp.StatusCode)
		}
		return
	}, httpBackOff)

	if err != nil {
		return errors.Wrap(err, "failed to send log parts with retries")
	}

	return nil
}

func (lps *httpLogPartSink) generatePayloadSignature(payload []*httpLogPartEncodedPayload) string {
	authTokens := []string{}
	for _, logPart := range payload {
		authTokens = append(authTokens, logPart.Token)
	}

	sig := sha1.Sum([]byte(strings.Join(authTokens, "")))
	return fmt.Sprintf("%s", hex.EncodeToString(sig[:]))
}
