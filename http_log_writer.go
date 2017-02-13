package worker

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	gocontext "context"

	"github.com/Sirupsen/logrus"
	"github.com/jtacoma/uritemplates"
	"github.com/pkg/errors"
	"github.com/travis-ci/worker/context"
)

type httpLogPart struct {
	JobID   uint64 `json:"id"`
	Content string `json:"log"`
	Number  int    `json:"number"`
	UUID    string `json:"uuid"`
	Final   bool   `json:"final"`
}

type httpLogWriter struct {
	ctx   gocontext.Context
	jobID uint64

	closeChan chan struct{}

	bufferMutex   sync.Mutex
	buffer        *bytes.Buffer
	logPartNumber int

	bytesWritten int
	maxLength    int

	httpClient *http.Client
	baseURL    string
	authToken  string

	timer   *time.Timer
	timeout time.Duration
}

func newHTTPLogWriter(ctx gocontext.Context, url string, authToken string, jobID uint64, timeout time.Duration) (*httpLogWriter, error) {
	return &httpLogWriter{
		ctx:        context.FromComponent(ctx, "log_writer"),
		jobID:      jobID,
		closeChan:  make(chan struct{}),
		buffer:     new(bytes.Buffer),
		timer:      time.NewTimer(time.Hour),
		timeout:    timeout,
		httpClient: &http.Client{},
		baseURL:    url,
		authToken:  authToken,
	}, nil
}

func (w *httpLogWriter) Write(p []byte) (int, error) {
	if w.closed() {
		return 0, fmt.Errorf("attempted write to closed log")
	}

	context.LoggerFromContext(w.ctx).WithFields(logrus.Fields{
		"length": len(p),
		"bytes":  string(p),
	}).Debug("writing bytes")

	w.timer.Reset(w.timeout)

	w.bytesWritten += len(p)
	if w.bytesWritten > w.maxLength {
		_, err := w.WriteAndClose([]byte(fmt.Sprintf("\n\nThe log length has exceeded the limit of %d MB (this usually means that the test suite is raising the same exception over and over).\n\nThe job has been terminated\n", w.maxLength/1000/1000)))
		if err != nil {
			context.LoggerFromContext(w.ctx).WithField("err", err).Error("couldn't write 'log length exceeded' error message to log")
		}
		return 0, ErrWrotePastMaxLogLength
	}

	w.bufferMutex.Lock()
	defer w.bufferMutex.Unlock()
	return w.buffer.Write(p)
}

func (w *httpLogWriter) Close() error {
	if w.closed() {
		return nil
	}

	w.timer.Stop()

	close(w.closeChan)
	w.flush()

	part := httpLogPart{
		JobID:  w.jobID,
		Number: w.logPartNumber,
		Final:  true,
	}
	w.logPartNumber++
	return w.publishLogPart(part)
}

func (w *httpLogWriter) Timeout() <-chan time.Time {
	return w.timer.C
}

func (w *httpLogWriter) SetMaxLogLength(bytes int) {
	w.maxLength = bytes
}

func (w *httpLogWriter) WriteAndClose(p []byte) (int, error) {
	if w.closed() {
		return 0, fmt.Errorf("log already closed")
	}

	w.timer.Stop()

	close(w.closeChan)

	w.bufferMutex.Lock()
	n, err := w.buffer.Write(p)
	w.bufferMutex.Unlock()
	if err != nil {
		return n, err
	}

	w.flush()

	part := httpLogPart{
		JobID:  w.jobID,
		Number: w.logPartNumber,
		Final:  true,
	}
	w.logPartNumber++

	err = w.publishLogPart(part)
	return n, err
}

func (w *httpLogWriter) closed() bool {
	select {
	case <-w.closeChan:
		return true
	default:
		return false
	}
}

func (w *httpLogWriter) flushRegularly() {
	ticker := time.NewTicker(LogWriterTick)
	defer ticker.Stop()
	for {
		select {
		case <-w.closeChan:
			return
		case <-ticker.C:
			w.flush()
		}
	}
}

func (w *httpLogWriter) flush() {
	if w.buffer.Len() <= 0 {
		return
	}

	buf := make([]byte, LogChunkSize)

	for w.buffer.Len() > 0 {
		w.bufferMutex.Lock()
		n, err := w.buffer.Read(buf)
		w.bufferMutex.Unlock()
		if err != nil {
			// According to documentation, err should only be non-nil if
			// there's no data in the buffer. We've checked for this, so
			// this means that err should never be nil. Something is very
			// wrong if this happens, so let's abort!
			panic("non-empty buffer shouldn't return an error on Read")
		}

		part := httpLogPart{
			JobID:   w.jobID,
			Content: string(buf[0:n]),
			Number:  w.logPartNumber,
		}
		w.logPartNumber++

		err = w.publishLogPart(part)
		if err != nil {
			context.LoggerFromContext(w.ctx).WithField("err", err).Error("couldn't publish log part")
		}
	}
}

func (w *httpLogWriter) publishLogPart(part httpLogPart) error {
	part.UUID, _ = context.UUIDFromContext(w.ctx)

	type logPartPayload struct {
		Type     string `json:"@type"`
		Final    bool   `json:"final"`
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}

	payload := logPartPayload{
		Type:     "log_part",
		Final:    part.Final,
		Content:  base64.StdEncoding.EncodeToString([]byte(part.Content)),
		Encoding: "base64",
	}

	template, err := uritemplates.Parse(w.baseURL)
	if err != nil {
		return errors.Wrap(err, "couldn't parse base URL template")
	}

	partURL, err := template.Expand(map[string]interface{}{
		"job_id":      w.jobID,
		"log_part_id": part.Number,
	})
	if err != nil {
		return errors.Wrap(err, "couldn't expand base URL template")
	}

	partBody, err := json.Marshal(payload)
	if err != nil {
		return errors.Wrap(err, "couldn't marshal JSON")
	}

	req, err := http.NewRequest("PUT", partURL, bytes.NewReader(partBody))
	if err != nil {
		return errors.Wrap(err, "couldn't create request")
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", w.authToken))

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "error making request")
	}

	if resp.StatusCode != http.StatusNoContent {
		return errors.Errorf("expected %d but got %d", http.StatusNoContent, resp.StatusCode)
	}

	return nil
}
