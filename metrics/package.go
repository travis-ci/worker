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
	metrics.GetOrRegisterTimer(name, metrics.DefaultRegistry).UpdateSince(since)
}

// TimeDuration increases the timer metric with the given name by the given duration
func TimeDuration(name string, duration time.Duration) {
	metrics.GetOrRegisterTimer(name, metrics.DefaultRegistry).Update(duration)
}

// Gauge sets a gauge metric to a given value
func Gauge(name string, value int64) {
	metrics.GetOrRegisterGauge(name, metrics.DefaultRegistry).Update(value)
}
