package main

import (
	"bufio"
	"io"
	"time"
)

type CoalesceWriteCloser struct {
	wc     io.WriteCloser
	buf    *bufio.Writer
	cancel chan bool
	err    error
}

func NewCoalesceWriteCloser(wc io.WriteCloser) *CoalesceWriteCloser {
	c := &CoalesceWriteCloser{
		wc:     wc,
		buf:    bufio.NewWriterSize(wc, 9216),
		cancel: make(chan bool),
	}

	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		for {
			select {
			case <-c.cancel:
				ticker.Stop()
				break
			case <-ticker.C:
				c.err = c.buf.Flush()
			}
		}
	}()

	return c
}

func (c *CoalesceWriteCloser) Write(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}

	return c.buf.Write(p)
}

func (c *CoalesceWriteCloser) Close() error {
	c.cancel <- true
	if c.err != nil {
		c.wc.Close()
		return c.err
	}

	err := c.buf.Flush()
	if err != nil {
		c.wc.Close()
		return err
	}

	return c.wc.Close()
}
