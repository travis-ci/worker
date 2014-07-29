package main

import (
	"fmt"
	"github.com/rcrowley/go-metrics"
	"github.com/rcrowley/go-metrics/librato"
	"time"
)

type Metrics interface {
	BootTimer(provider string, d time.Duration)
	MarkBootTimeout(provider string)
	MarkBootError(provider string)
	MarkAPIError(provider string, statusCode int)
	MarkJobRequeued()
	MarkCouldNotCloseSSH()
	StartLogging()
	EachMetric(func(string, interface{}))
}

type liveMetrics struct {
	registry metrics.Registry
}

func NewMetrics(source string, config LibratoConfig) Metrics {
	registry := metrics.NewRegistry()

	if config.Email != "" {
		go librato.Librato(
			registry,
			time.Minute,
			config.Email,
			config.Token,
			source,
			[]float64{0.5, 0.75, 0.95, 0.99, 0.999},
			time.Second,
		)
	}

	return &liveMetrics{registry}
}

func (m *liveMetrics) BootTimer(provider string, d time.Duration) {
	m.timer(fmt.Sprintf("worker.vm.provider.%s.boot", provider)).Update(d)
}

func (m *liveMetrics) MarkBootTimeout(provider string) {
	m.meter(fmt.Sprintf("worker.vm.provider.%s.boot.timeout", provider)).Mark(1)
}

func (m *liveMetrics) MarkBootError(provider string) {
	m.meter(fmt.Sprintf("worker.vm.provider.%s.boot.error", provider)).Mark(1)
}

func (m *liveMetrics) MarkAPIError(provider string, statusCode int) {
	m.meter(fmt.Sprintf("worker.vm.provider.%s.api.error.%d", provider, statusCode)).Mark(1)
}

func (m *liveMetrics) MarkJobRequeued() {
	m.meter("worker.job.requeue").Mark(1)
}

func (m *liveMetrics) MarkCouldNotCloseSSH() {
	m.meter("worker.vm.ssh.could_not_close").Mark(1)
}

func (m *liveMetrics) timer(name string) metrics.Timer {
	return metrics.GetOrRegisterTimer(name, m.registry)
}

func (m *liveMetrics) meter(name string) metrics.Meter {
	return metrics.GetOrRegisterMeter(name, m.registry)
}

func (m *liveMetrics) StartLogging() {
	go func() {
		for _ = range time.Tick(time.Minute) {
			logMetrics(m)
		}
	}()
}

func (m *liveMetrics) EachMetric(f func(string, interface{})) {
	m.registry.Each(f)
}

func logMetrics(m Metrics) {
	now := time.Now().Unix()
	m.EachMetric(func(name string, i interface{}) {
		switch m := i.(type) {
		case metrics.Counter:
			fmt.Printf("metriks: time=%d name=%s type=count count=%d\n", now, name, m.Count())
		case metrics.Gauge:
			fmt.Printf("metriks: time=%d name=%s type=gauge value=%d\n", now, name, m.Value())
		case metrics.Healthcheck:
			m.Check()
			fmt.Printf("metriks: time=%d name=%s type=healthcheck error=%v\n", now, name, m.Error())
		case metrics.Histogram:
			ps := m.Percentiles([]float64{0.5, 0.75, 0.95, 0.99, 0.999})
			fmt.Printf("metriks: time=%d name=%s type=histogram count=%d min=%f max=%f mean=%f stddev=%f median=%f 95th_percentile=%f 99th_percentile=%f\n", now, name, m.Count(), int64NanoToSeconds(m.Min()), int64NanoToSeconds(m.Max()), float64NanoToSeconds(m.Mean()), float64NanoToSeconds(m.StdDev()), float64NanoToSeconds(ps[0]), float64NanoToSeconds(ps[2]), float64NanoToSeconds(ps[3]))
		case metrics.Meter:
			fmt.Printf("metriks: time=%d name=%s type=meter count=%d one_minute_rate=%f five_minute_rate=%f fifteen_minute_rate=%f mean_rate=%f\n", now, name, m.Count(), m.Rate1(), m.Rate5(), m.Rate15(), m.RateMean())
		case metrics.Timer:
			ps := m.Percentiles([]float64{0.5, 0.75, 0.95, 0.99, 0.999})
			fmt.Printf("metriks: time=%d name=%s type=timer count=%d one_minute_rate=%f five_minute_rate=%f fifteen_minute_rate=%f mean_rate=%f min=%f max=%f mean=%f stddev=%f median=%f 95th_percentile=%f 99th_percentile=%f\n", now, name, m.Count(), m.Rate1(), m.Rate5(), m.Rate15(), m.RateMean(), int64NanoToSeconds(m.Min()), int64NanoToSeconds(m.Max()), float64NanoToSeconds(m.Mean()), float64NanoToSeconds(m.StdDev()), float64NanoToSeconds(ps[0]), float64NanoToSeconds(ps[2]), float64NanoToSeconds(ps[3]))
		}
	})
}

func int64NanoToSeconds(d int64) float64 {
	return float64(d) / float64(time.Second)
}

func float64NanoToSeconds(d float64) float64 {
	return d / float64(time.Second)
}
