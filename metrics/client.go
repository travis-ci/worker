package metrics

import (
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/henrikhodne/go-librato/librato"
)

type Client struct {
	lc       *librato.Client
	source   string
	max      uint
	measures chan interface{}
	done     chan struct{}
	st       time.Duration
	pm       sync.Mutex
}

func NewClient(email, token, source string, sampleTimeout, publishTick time.Duration,
	max uint, log *logrus.Entry) *Client {

	c := &Client{
		lc:       librato.NewClient(email, token),
		log:      log.WithField("self", "metrics_client"),
		source:   source,
		max:      max,
		measures: make(chan interface{}, max),
		done:     make(chan struct{}),
		st:       sampleTimeout,
		pm:       sync.Mutex{},
	}
	go c.run(publishTick)
	return c
}

func (cl *Client) Close() {
	select {
	case cl.done <- struct{}{}:
	case <-time.After(10 * time.Second):
		cl.log.Error("failed to close within 10s")
	}
}

func (cl *Client) AddGauge(g *librato.GaugeMeasurement) {
	if g.MeasureTime == nil {
		g.MeasureTime = librato.Uint(uint(time.Now().Unix()))
	}
	select {
	case l.measures <- g:
	default:
		cl.log.Error("failed to add gauge measurement")
	}
}

func (cl *Client) AddCounter(c *librato.Measurement) {
	if c.MeasureTime == nil {
		c.MeasureTime = librato.Uint(uint(time.Now().Unix()))
	}
	select {
	case l.measures <- c:
	default:
		cl.log.Error("failed to add counter measurement")
	}
}

func (cl *Client) run(publishTick time.Duration) {
	t := time.NewTicker(publishTick)

	for {
		select {
		case <-t.C:
			cl.push()
		case <-c.done:
			t.Stop()
			cl.push()
			return
		}
	}
}

func (cl *Client) push() {
	cl.pm.Lock()
	defer cl.pm.Unlock()

	s := cl.takeSample()
	if s == nil {
		return
	}

	gauges := []interface{}{}
	counters := []interface{}{}

	ms := &librato.MeasurementSubmissions{
		MeasureTime: librato.Uint(uint(time.Now().UTC().Unix())),
		Source:      librato.String(cl.source),
		Gauges:      s.gauges,
		Counters:    s.counters,
	}

	resp, err := cl.lc.Metrics.Create(ms)
	defer func() {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	}()

	if err != nil {
		cl.log.WithField("err", err).Error("failed to publish metrics")
	}
}

func (cl *Client) takeSample() sample {
	s := sample{
		gauges:   []interface{}{},
		counters: []interface{}{},
	}

	done := make(chan struct{})
	cancel := make(chan struct{})
	defer func() {
		cancel <- struct{}{}
	}()

	func() {
		for i := 0; i < cl.max; i++ {
			select {
			case m := <-cl.measures:
				switch m.(type) {
				case *librato.GaugeMeasurement:
					s.gauges = append(s.gauges, m.(*librato.GaugeMeasurement))
				case *librato.Measurement:
					s.counters = append(s.counters, m.(*librato.Measurement))
				}
			case <-cancel:
				return
			default:
				done <- struct{}{}
				return
			}
		}
	}()

	select {
	case <-time.After(cl.st):
	case <-done:
	}

	return s
}

type sample struct {
	gauges   []interface{}
	counters []interface{}
}
