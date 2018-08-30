package worker

import (
	"fmt"
	"time"

	gocontext "context"

	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/context"
)

type httpLogPart struct {
	Content string
	Final   bool
	JobID   uint64
	Number  uint64
	Token   string
}

type httpLogWriter struct {
	ctx       gocontext.Context
	jobID     uint64
	authToken string

	closeChan chan struct{}

	logPartNumber uint64

	bytesWritten int
	maxLength    int

	lps *httpLogPartSink

	timer   *time.Timer
	timeout time.Duration
}

func newHTTPLogWriter(ctx gocontext.Context, url string, authToken string, jobID uint64, timeout time.Duration) (*httpLogWriter, error) {
	writer := &httpLogWriter{
		ctx:       context.FromComponent(ctx, "log_writer"),
		jobID:     jobID,
		authToken: authToken,
		closeChan: make(chan struct{}),
		timer:     time.NewTimer(time.Hour),
		timeout:   timeout,
		lps:       getHTTPLogPartSinkByURL(url),
	}

	return writer, nil
}

func (w *httpLogWriter) Write(p []byte) (int, error) {
	if w.closed() {
		return 0, fmt.Errorf("attempted write to closed log")
	}

	logger := context.LoggerFromContext(w.ctx).WithFields(logrus.Fields{
		"self": "http_log_writer",
		"inst": fmt.Sprintf("%p", w),
	})

	logger.WithFields(logrus.Fields{
		"length": len(p),
		"bytes":  string(p),
	}).Debug("begin writing bytes")

	w.timer.Reset(w.timeout)

	w.bytesWritten += len(p)
	if w.bytesWritten > w.maxLength {
		_, err := w.WriteAndClose([]byte(fmt.Sprintf("\n\nThe log length has exceeded the limit of %d MB (this usually means that the test suite is raising the same exception over and over).\n\nThe job has been terminated\n", w.maxLength/1000/1000)))
		if err != nil {
			logger.WithField("err", err).Error("couldn't write 'log length exceeded' error message to log")
		}
		return 0, ErrWrotePastMaxLogLength
	}

	err := w.lps.Add(w.ctx, &httpLogPart{
		Content: string(p),
		JobID:   w.jobID,
		Number:  w.logPartNumber,
		Token:   w.authToken,
	})
	if err != nil {
		context.LoggerFromContext(w.ctx).WithFields(logrus.Fields{
			"err":  err,
			"self": "http_log_writer",
		}).Error("could not add log part to sink")
		return 0, err
	}

	w.logPartNumber++
	return len(p), err
}

func (w *httpLogWriter) Close() error {
	if w.closed() {
		return nil
	}

	w.timer.Stop()

	close(w.closeChan)

	err := w.lps.Add(w.ctx, &httpLogPart{
		Final:  true,
		JobID:  w.jobID,
		Number: w.logPartNumber,
		Token:  w.authToken,
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

func (w *httpLogWriter) SetJobStarted() {}

func (w *httpLogWriter) SetCancelFunc(cancel gocontext.CancelFunc) {}

func (w *httpLogWriter) MaxLengthReached() bool {
	return false
}

func (w *httpLogWriter) WriteAndClose(p []byte) (int, error) {
	if w.closed() {
		return 0, fmt.Errorf("log already closed")
	}

	w.timer.Stop()

	close(w.closeChan)

	err := w.lps.Add(w.ctx, &httpLogPart{
		Content: string(p),
		JobID:   w.jobID,
		Number:  w.logPartNumber,
		Token:   w.authToken,
	})

	if err != nil {
		context.LoggerFromContext(w.ctx).WithFields(logrus.Fields{
			"err":  err,
			"self": "http_log_writer",
		}).Error("could not add log part to sink")
		return 0, err
	}
	w.logPartNumber++

	err = w.lps.Add(w.ctx, &httpLogPart{
		Final:  true,
		JobID:  w.jobID,
		Number: w.logPartNumber,
		Token:  w.authToken,
	})

	if err != nil {
		context.LoggerFromContext(w.ctx).WithFields(logrus.Fields{
			"err":  err,
			"self": "http_log_writer",
		}).Error("could not add log part to sink")
		return 0, err
	}
	w.logPartNumber++
	return len(p), nil
}

func (w *httpLogWriter) closed() bool {
	select {
	case <-w.closeChan:
		return true
	default:
		return false
	}
}
