package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/streadway/amqp"
	"golang.org/x/net/context"
)

var (
	LogWriterTick = 500 * time.Millisecond

	// This is a bit of a magic number, calculated like this: The maximum
	// Pusher payload is 10 kB (or 10 KiB, who knows, but let's go with 10
	// kB since that is smaller). Looking at the travis-logs source, the
	// current message overhead (i.e. the part of the payload that isn't
	// the content of the log part) is 42 bytes + the length of the JSON-
	// encoded ID and the length of the JSON-encoded sequence number. A 64-
	// bit number is up to 20 digits long, so that means (assuming we don't
	// go over 64-bit numbers) the overhead is up to 82 bytes. That means
	// we can send up to 9918 bytes of content. However, the JSON-encoded
	// version of a string can be significantly longer than the raw bytes.
	// Worst case that I could find is "<", which with the Go JSON encoder
	// becomes "\u003c" (i.e. six bytes long). So, given a string of just
	// left angle brackets, the string would become six times as long,
	// meaning that the longest string we can take is 1653. We could still
	// get errors if we go over 64-bit numbers, but I find the likeliness
	// of that happening to both the sequence number, the ID, and us maxing
	// out the worst-case logs to be quite unlikely, so I'm willing to live
	// with that. --Henrik
	LogChunkSize = 1653
)

// A LogWriter is an io.WriteCloser that redirects to travis-logs
type LogWriter struct {
	ctx      context.Context
	amqpConn *amqp.Connection
	jobID    uint64

	closeChan chan struct{}

	bufferMutex   sync.Mutex
	buffer        *bytes.Buffer
	logPartNumber int

	amqpChanMutex sync.RWMutex
	amqpChan      *amqp.Channel
}

type logPart struct {
	JobID   uint64 `json:"id"`
	Content string `json:"log"`
	Number  int    `json:"number"`
	UUID    string `json:"uuid"`
	Final   bool   `json:"final"`
}

func NewLogWriter(ctx context.Context, conn *amqp.Connection, jobID uint64) (*LogWriter, error) {
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

	writer := &LogWriter{
		ctx:       contextFromComponent(ctx, "log_writer"),
		amqpConn:  conn,
		amqpChan:  channel,
		jobID:     jobID,
		closeChan: make(chan struct{}),
		buffer:    new(bytes.Buffer),
	}

	go writer.flushRegularly()

	return writer, nil
}

func (w *LogWriter) Write(p []byte) (int, error) {
	if w.closed() {
		return 0, fmt.Errorf("attempted write to closed log")
	}

	w.bufferMutex.Lock()
	defer w.bufferMutex.Unlock()
	return w.buffer.Write(p)
}

func (w *LogWriter) Close() error {
	if w.closed() {
		return nil
	}

	close(w.closeChan)
	w.flush()

	part := logPart{
		JobID:  w.jobID,
		Number: w.logPartNumber,
		Final:  true,
	}
	w.logPartNumber++

	return w.publishLogPart(part)
}

// WriteAndClose works like a Write followed by a Close, but ensures that no
// other Writes are allowed in between.
func (w *LogWriter) WriteAndClose(p []byte) (int, error) {
	if w.closed() {
		return 0, fmt.Errorf("log already closed")
	}

	close(w.closeChan)

	w.bufferMutex.Lock()
	n, err := w.buffer.Write(p)
	w.bufferMutex.Unlock()
	if err != nil {
		return n, err
	}

	w.flush()

	part := logPart{
		JobID:  w.jobID,
		Number: w.logPartNumber,
		Final:  true,
	}
	w.logPartNumber++

	return n, w.publishLogPart(part)
}

func (w *LogWriter) closed() bool {
	select {
	case <-w.closeChan:
		return true
	default:
		return false
	}
}

func (w *LogWriter) flushRegularly() {
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

func (w *LogWriter) flush() {
	w.bufferMutex.Lock()
	defer w.bufferMutex.Unlock()

	if w.buffer.Len() <= 0 {
		return
	}

	buf := make([]byte, LogChunkSize)

	for w.buffer.Len() > 0 {
		n, err := w.buffer.Read(buf)
		if err != nil {
			// According to documentation, err should only be non-nil if
			// there's no data in the buffer. We've checked for this, so
			// this means that err should never be nil. Something is very
			// wrong if this happens, so let's abort!
			panic("non-empty buffer shouldn't return an error on Read")
		}

		part := logPart{
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
					LoggerFromContext(w.ctx).WithField("err", err).Error("couldn't publish log part and couldn't reopen channel")
					// Close or something
					return
				}

				err = w.publishLogPart(part)
				LoggerFromContext(w.ctx).WithField("err", err).Error("couldn't publish log part, even after reopening channel")
			default:
				LoggerFromContext(w.ctx).WithField("err", err).Error("couldn't publish log part")
			}
		}
	}
}

func (w *LogWriter) publishLogPart(part logPart) error {
	part.UUID, _ = uuidFromContext(w.ctx)

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

func (w *LogWriter) reopenChannel() error {
	w.amqpChanMutex.Lock()
	defer w.amqpChanMutex.Unlock()

	amqpChan, err := w.amqpConn.Channel()
	if err != nil {
		return err
	}

	// reopenChannel() shouldn't be called if the channel isn't already closed.
	// but we're closing the channel again, just in case, to avoid leaking
	// channels.
	w.amqpChan.Close()

	w.amqpChan = amqpChan

	return nil
}
