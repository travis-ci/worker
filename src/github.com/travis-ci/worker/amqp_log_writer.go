package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/streadway/amqp"
	"github.com/travis-ci/worker/context"
	gocontext "golang.org/x/net/context"
)

type amqpLogPart struct {
	JobID   uint64 `json:"id"`
	Content string `json:"log"`
	Number  int    `json:"number"`
	UUID    string `json:"uuid"`
	Final   bool   `json:"final"`
}

type amqpLogWriter struct {
	ctx      gocontext.Context
	amqpConn *amqp.Connection
	jobID    uint64

	closeChan chan struct{}

	bufferMutex   sync.Mutex
	buffer        *bytes.Buffer
	logPartNumber int

	bytesWritten int
	maxLength    int

	amqpChanMutex sync.RWMutex
	amqpChan      *amqp.Channel

	timer   *time.Timer
	timeout time.Duration
}

func newAMQPLogWriter(ctx gocontext.Context, conn *amqp.Connection, jobID uint64) (*amqpLogWriter, error) {
	channel, err := conn.Channel()
	if err != nil {
		return nil, err
	}

	err = channel.ExchangeDeclare("reporting", "topic", true, false, false, false, nil)
	if err != nil {
		return nil, err
	}

	_, err = channel.QueueDeclare("reporting.jobs.logs", true, false, false, false, nil)
	if err != nil {
		return nil, err
	}

	err = channel.QueueBind("reporting.jobs.logs", "reporting.jobs.logs", "reporting", false, nil)
	if err != nil {
		return nil, err
	}

	writer := &amqpLogWriter{
		ctx:       context.FromComponent(ctx, "log_writer"),
		amqpConn:  conn,
		amqpChan:  channel,
		jobID:     jobID,
		closeChan: make(chan struct{}),
		buffer:    new(bytes.Buffer),
		timer:     time.NewTimer(time.Hour),
		timeout:   0,
	}

	context.LoggerFromContext(ctx).WithFields(logrus.Fields{
		"writer": writer,
		"job_id": jobID,
	}).Debug("created new log writer")

	go writer.flushRegularly()

	return writer, nil
}

func (w *amqpLogWriter) WriteFold(name string, b []byte) (int, error) {
	folded := []byte(fmt.Sprintf("travis_fold start %s\n", name))
	folded = append(folded, b...)

	if string(folded[len(folded)-1]) != "\n" {
		folded = append(folded, []byte("\n")...)
	}

	folded = append(folded, []byte(fmt.Sprintf("travis_fold end %s\n", name))...)
	return w.Write(folded)
}

func (w *amqpLogWriter) Write(p []byte) (int, error) {
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

func (w *amqpLogWriter) Close() error {
	if w.closed() {
		return nil
	}

	w.timer.Stop()

	close(w.closeChan)
	w.flush()

	part := amqpLogPart{
		JobID:  w.jobID,
		Number: w.logPartNumber,
		Final:  true,
	}
	w.logPartNumber++

	err := w.publishLogPart(part)
	_ = w.amqpChan.Close()
	return err
}

func (w *amqpLogWriter) SetTimeout(d time.Duration) {
	w.timeout = d
	w.timer.Reset(w.timeout)
}

func (w *amqpLogWriter) Timeout() <-chan time.Time {
	return w.timer.C
}

func (w *amqpLogWriter) SetMaxLogLength(bytes int) {
	w.maxLength = bytes
}

// WriteAndClose works like a Write followed by a Close, but ensures that no
// other Writes are allowed in between.
func (w *amqpLogWriter) WriteAndClose(p []byte) (int, error) {
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

	part := amqpLogPart{
		JobID:  w.jobID,
		Number: w.logPartNumber,
		Final:  true,
	}
	w.logPartNumber++

	err = w.publishLogPart(part)
	_ = w.amqpChan.Close()
	return n, err
}

func (w *amqpLogWriter) closed() bool {
	select {
	case <-w.closeChan:
		return true
	default:
		return false
	}
}

func (w *amqpLogWriter) flushRegularly() {
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

func (w *amqpLogWriter) flush() {
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

		part := amqpLogPart{
			JobID:   w.jobID,
			Content: string(buf[0:n]),
			Number:  w.logPartNumber,
		}
		w.logPartNumber++

		err = w.publishLogPart(part)
		if err != nil {
			switch err.(type) {
			case *amqp.Error:
				if w.reopenChannel() != nil {
					context.LoggerFromContext(w.ctx).WithField("err", err).Error("couldn't publish log part and couldn't reopen channel")
					// Close or something
					return
				}

				err = w.publishLogPart(part)
				context.LoggerFromContext(w.ctx).WithField("err", err).Error("couldn't publish log part, even after reopening channel")
			default:
				context.LoggerFromContext(w.ctx).WithField("err", err).Error("couldn't publish log part")
			}
		}
	}
}

func (w *amqpLogWriter) publishLogPart(part amqpLogPart) error {
	part.UUID, _ = context.UUIDFromContext(w.ctx)

	partBody, err := json.Marshal(part)
	if err != nil {
		return err
	}

	w.amqpChanMutex.RLock()
	err = w.amqpChan.Publish("reporting", "reporting.jobs.logs", false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
		Type:         "job:test:log",
		Body:         partBody,
	})
	w.amqpChanMutex.RUnlock()

	return err
}

func (w *amqpLogWriter) reopenChannel() error {
	w.amqpChanMutex.Lock()
	defer w.amqpChanMutex.Unlock()

	amqpChan, err := w.amqpConn.Channel()
	if err != nil {
		return err
	}

	// reopenChannel() shouldn't be called if the channel isn't already closed.
	// but we're closing the channel again, just in case, to avoid leaking
	// channels.
	_ = w.amqpChan.Close()

	w.amqpChan = amqpChan

	return nil
}
