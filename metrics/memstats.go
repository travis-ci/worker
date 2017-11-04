package metrics

import (
	"fmt"
	"runtime"
	"time"
)

// ReportMemstatsMetrics will send runtime Memstats metrics every 10 seconds,
// and will block forever.
func ReportMemstatsMetrics() {
	memStats := &runtime.MemStats{}
	lastSampleTime := time.Now()
	var lastPauseNs uint64
	var lastNumGC uint64

	sleep := 10 * time.Second

	for {
		runtime.ReadMemStats(memStats)
		now := time.Now()

		for n, c := range map[string]uint64{
			"goroutines":            uint64(runtime.NumGoroutine()),
			"memory.allocated":      memStats.Alloc,
			"memory.mallocs":        memStats.Mallocs,
			"memory.frees":          memStats.Frees,
			"memory.gc.total_pause": memStats.PauseTotalNs,
			"memory.gc.heap":        memStats.HeapAlloc,
			"memory.gc.stack":       memStats.StackInuse,
		} {
			Gauge(fmt.Sprintf("travis.worker.%s", n), int64(c))
		}

		if lastPauseNs > 0 {
			pauseSinceLastSample := memStats.PauseTotalNs - lastPauseNs
			Gauge("travis.worker.memory.gc.pause_per_second",
				int64(float64(pauseSinceLastSample)/sleep.Seconds()))
		}
		lastPauseNs = memStats.PauseTotalNs

		countGC := int(uint64(memStats.NumGC) - lastNumGC)
		if lastNumGC > 0 {
			diff := float64(countGC)
			diffTime := now.Sub(lastSampleTime).Seconds()
			Gauge("travis.worker.memory.gc.gc_per_second", int64(diff/diffTime))
		}

		if countGC > 0 {
			if countGC > 256 {
				countGC = 256
			}

			for i := 0; i < countGC; i++ {
				idx := int((memStats.NumGC-uint32(i))+255) % 256
				pause := time.Duration(memStats.PauseNs[idx])
				Gauge("travis.worker.memory.gc.pause", int64(pause))
			}
		}

		lastNumGC = uint64(memStats.NumGC)
		lastSampleTime = now

		time.Sleep(sleep)
	}
}
