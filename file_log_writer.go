package worker

import (
	"os"
	"time"

	gocontext "golang.org/x/net/context"
)

type fileLogWriter struct {
	ctx     gocontext.Context
	logFile string
	fd      *os.File

	timer   *time.Timer
	timeout time.Duration
}

func newFileLogWriter(ctx gocontext.Context, logFile string) (LogWriter, error) {
	fd, err := os.Create(logFile)
	if err != nil {
		return nil, err
	}

	return &fileLogWriter{
		ctx:     ctx,
		logFile: logFile,
		fd:      fd,

		timer:   time.NewTimer(time.Hour),
		timeout: 0,
	}, nil
}

func (w *fileLogWriter) Write(b []byte) (int, error) {
	return w.fd.Write(b)
}

func (w *fileLogWriter) Close() error {
	return w.fd.Close()
}

func (w *fileLogWriter) SetMaxLogLength(n int) {
	return
}

func (w *fileLogWriter) SetTimeout(d time.Duration) {
	w.timeout = d
	w.timer.Reset(w.timeout)
}

func (w *fileLogWriter) Timeout() <-chan time.Time {
	return w.timer.C
}

func (w *fileLogWriter) WriteAndClose(b []byte) (int, error) {
	n, err := w.Write(b)
	if err != nil {
		return n, err
	}

	err = w.Close()
	return n, err
}
