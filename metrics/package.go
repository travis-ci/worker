// Package metrics provides easy methods to send metrics
package metrics

import (
	"time"

	"github.com/rcrowley/go-metrics"
)

// Mark increases the meter metric with the given name by 1
func Mark(name string) {
	metrics.GetOrRegisterMeter(name, metrics.DefaultRegistry).Mark(1)
}

// TimeSince increases the timer metric with the given name by the time since the given time
func TimeSince(name string, since time.Time) {
	metrics.DefaultRegistry.Register(name, newTimerWithSmallerSampleSize)

	metrics.GetOrRegisterTimer(name, metrics.DefaultRegistry).UpdateSince(since)
}

// TimeDuration increases the timer metric with the given name by the given duration
func TimeDuration(name string, duration time.Duration) {
	metrics.DefaultRegistry.Register(name, newTimerWithSmallerSampleSize)

	metrics.GetOrRegisterTimer(name, metrics.DefaultRegistry).Update(duration)
}

// Gauge sets a gauge metric to a given value
func Gauge(name string, value int64) {
	metrics.GetOrRegisterGauge(name, metrics.DefaultRegistry).Update(value)
}

// the default Timer implementation from go-metrics has a large sample size of 1028
// this skews the results towards high values when we don't have that much throughput
// to get more accurate percentiles in worker, we will use a smaller sample size of 50
func newTimerWithSmallerSampleSize() metrics.Timer {
	return metrics.NewCustomTimer(
		metrics.NewHistogram(metrics.NewExpDecaySample(50, 0.015)),
		metrics.NewMeter(),
	)
}
