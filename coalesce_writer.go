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
	write  chan writeReq
	err    error
}

type writeRes struct {
	n   int
	err error
}

type writeReq struct {
	buf []byte
	res chan writeRes
}

func NewCoalesceWriteCloser(wc io.WriteCloser) *CoalesceWriteCloser {
	c := &CoalesceWriteCloser{
		wc:     wc,
		buf:    bufio.NewWriterSize(wc, 9216),
		cancel: make(chan bool),
		write:  make(chan writeReq),
	}

	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		for {
			select {
			case <-c.cancel:
				ticker.Stop()
				return
			case wreq := <-c.write:
				n, err := c.buf.Write(wreq.buf)
				wreq.res <- writeRes{n, err}
			case <-ticker.C:
				c.err = c.buf.Flush()
				if c.err != nil {
					ticker.Stop()
					return
				}
			}
		}
	}()

	return c
}

func (c *CoalesceWriteCloser) Write(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}

	wreq := writeReq{p, make(chan writeRes)}
	c.write <- wreq
	wres := <-wreq.res
	return wres.n, wres.err
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
