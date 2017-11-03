package metrics

import (
	"fmt"
	"runtime"
	"time"

	"github.com/henrikhodne/go-librato/librato"
)

// ReportMemstatsMetrics will send runtime Memstats metrics every 10 seconds,
// and will block forever.
func ReportMemstatsMetrics(c *Client) {
	memStats := &runtime.MemStats{}
	lastSampleTime := time.Now()
	var lastPauseNs uint64
	var lastNumGC uint64

	sleep := 10 * time.Second

	for {
		runtime.ReadMemStats(memStats)
		now := time.Now()

		for n, c := range map[string]interface{}{
			"goroutines":            runtime.NumGoroutine(),
			"memory.allocated":      memStats.Alloc,
			"memory.mallocs":        memStats.Mallocs,
			"memory.frees":          memStats.Frees,
			"memory.gc.total_pause": memStats.PauseTotalNs,
			"memory.gc.heap":        memStats.HeapAlloc,
			"memory.gc.stack":       memStats.StackInuse,
		} {
			c.AddGauge(&librato.GaugeMeasurement{
				Name:  fmt.Sprintf("travis.worker.%s", n),
				Count: librato.Uint(uint(c)),
			})
		}

		if lastPauseNs > 0 {
			pauseSinceLastSample := memStats.PauseTotalNs - lastPauseNs
			c.AddGauge(&librato.GaugeMeasurement{
				Name:  "travis.worker.memory.gc.pause_per_second",
				Count: librato.Uint(uint(float64(pauseSinceLastSample) / sleep.Seconds())),
			})
		}
		lastPauseNs = memStats.PauseTotalNs

		countGC := int(uint64(memStats.NumGC) - lastNumGC)
		if lastNumGC > 0 {
			diff := float64(countGC)
			diffTime := now.Sub(lastSampleTime).Seconds()
			c.AddGauge(&librato.GaugeMeasurement{
				Name:  "travis.worker.memory.gc.gc_per_second",
				Count: librato.Uint(uint(diff / diffTime)),
			})
		}

		// TODO: do this the go-librato way
		// if countGC > 0 {
		// if countGC > 256 {
		// countGC = 256
		// }

		// for i := 0; i < countGC; i++ {
		// idx := int((memStats.NumGC-uint32(i))+255) % 256
		// pause := time.Duration(memStats.PauseNs[idx])
		// metrics.GetOrRegisterTimer("travis.worker.memory.gc.pause", metrics.DefaultRegistry).Update(pause)
		// }
		// }

		lastNumGC = uint64(memStats.NumGC)
		lastSampleTime = now

		time.Sleep(sleep)
	}
}
