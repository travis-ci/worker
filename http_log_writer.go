package worker

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	gocontext "context"

	"github.com/Sirupsen/logrus"
	"github.com/travis-ci/worker/context"
)

type httpLogPart struct {
	JobID   uint64 `json:"id"`
	Content string `json:"log"`
	Number  int    `json:"number"`
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

	lps *httpLogPartSink

	timer   *time.Timer
	timeout time.Duration
}

type httpLogPartPayload struct {
	Type     string `json:"@type"`
	JobID    uint64 `json:"job_id"`
	Final    bool   `json:"final"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

func newHTTPLogWriter(ctx gocontext.Context, url string, authToken string, jobID uint64, timeout time.Duration) (*httpLogWriter, error) {
	defaultHTTPLogPartSinkMutex.Lock()
	defer defaultHTTPLogPartSinkMutex.Unlock()

	if defaultHTTPLogPartSink == nil {
		defaultHTTPLogPartSink = newHTTPLogPartSink(ctx, url, authToken, defaultHTTPLogPartSinkMaxBufferSize)
	}

	writer := &httpLogWriter{
		ctx:       context.FromComponent(ctx, "log_writer"),
		jobID:     jobID,
		closeChan: make(chan struct{}),
		buffer:    new(bytes.Buffer),
		timer:     time.NewTimer(time.Hour),
		timeout:   timeout,
		lps:       defaultHTTPLogPartSink,
	}

	go writer.flushRegularly(ctx)

	return writer, nil
}

func (w *httpLogWriter) Write(p []byte) (int, error) {
	if w.closed() {
		return 0, fmt.Errorf("attempted write to closed log")
	}

	logger := context.LoggerFromContext(w.ctx).WithField("self", "http_log_writer")

	logger.WithFields(logrus.Fields{
		"length": len(p),
		"bytes":  string(p),
	}).Debug("writing bytes")

	w.timer.Reset(w.timeout)

	w.bytesWritten += len(p)
	if w.bytesWritten > w.maxLength {
		_, err := w.WriteAndClose([]byte(fmt.Sprintf("\n\nThe log length has exceeded the limit of %d MB (this usually means that the test suite is raising the same exception over and over).\n\nThe job has been terminated\n", w.maxLength/1000/1000)))
		if err != nil {
			logger.WithField("err", err).Error("couldn't write 'log length exceeded' error message to log")
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

	err := w.lps.Add(&httpLogPart{
		JobID:  w.jobID,
		Number: w.logPartNumber,
		Final:  true,
	})

	if err != nil {
		context.LoggerFromContext(w.ctx).WithFields(logrus.Fields{
			"err":  err,
			"self": "http_log_writer",
		}).Error("could not add log part to sink")
		return err
	}

	w.logPartNumber++
	return nil
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

	part := &httpLogPart{
		JobID:  w.jobID,
		Number: w.logPartNumber,
		Final:  true,
	}

	err = w.lps.Add(part)

	if err != nil {
		context.LoggerFromContext(w.ctx).WithFields(logrus.Fields{
			"err":  err,
			"self": "http_log_writer",
		}).Error("could not add log part to sink")
		return n, err
	}
	w.logPartNumber++
	return n, nil
}

func (w *httpLogWriter) closed() bool {
	select {
	case <-w.closeChan:
		return true
	default:
		return false
	}
}

func (w *httpLogWriter) flushRegularly(ctx gocontext.Context) {
	ticker := time.NewTicker(LogWriterTick)
	defer ticker.Stop()
	for {
		select {
		case <-w.closeChan:
			return
		case <-ticker.C:
			w.flush()
		case <-ctx.Done():
			return
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

		err = w.lps.Add(&httpLogPart{
			JobID:   w.jobID,
			Content: string(buf[0:n]),
			Number:  w.logPartNumber,
		})
		if err != nil {
			context.LoggerFromContext(w.ctx).WithFields(logrus.Fields{
				"err":  err,
				"self": "http_log_writer",
			}).Error("could not add log part to sink")
			return
		}
		w.logPartNumber++
	}
}
