// Package metrics provides easy methods to send metrics
package metrics

import (
	"github.com/henrikhodne/go-librato/librato"
)

// Mark increases the meter metric with the given name by 1
func Mark(name string) {
	if DefaultClient == nil {
		return
	}
	DefaultClient.AddCounter(&librato.Measurement{
		Name:  name,
		Value: librato.Float(float64(1)),
	})
}

// Gauge sets a gauge metric to a given value
func Gauge(name string, value int64) {
	if DefaultClient == nil {
		return
	}
	DefaultClient.AddGauge(&librato.GaugeMeasurement{
		Measurement: &librato.Measurement{Name: name},
		Count:       librato.Uint(uint(value)),
	})
}
