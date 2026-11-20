// Package membench measures the memory that a benchmark uses: the heap that
// stays live after the work, the highest heap in use during the work, and the
// total number of bytes that the work allocates. A benchmark that reads these
// values shows a cost that bytes per operation alone does not show, for
// example a list that is copied several times.
package membench

import (
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// sampleEvery is the time between two reads of the heap while a meter runs.
const sampleEvery = 5 * time.Millisecond

// mib is the number of bytes in one MiB.
const mib = 1 << 20

// Meter measures one run of the work of a benchmark.
type Meter struct {
	before runtime.MemStats
	peak   atomic.Uint64
	stop   chan struct{}
	done   sync.WaitGroup
}

// Start collects the garbage, records the heap and starts to sample it. Call
// Stop after the work.
func Start() *Meter {
	m := new(Meter)
	m.stop = make(chan struct{})

	debug.FreeOSMemory()
	runtime.ReadMemStats(&m.before)

	m.done.Go(func() {
		tick := time.NewTicker(sampleEvery)
		defer tick.Stop()

		var stats runtime.MemStats

		for {
			select {
			case <-m.stop:
				return
			case <-tick.C:
				runtime.ReadMemStats(&stats)

				if stats.HeapInuse > m.peak.Load() {
					m.peak.Store(stats.HeapInuse)
				}
			}
		}
	})

	return m
}

// Stop ends the sampling and reports the three values on b, in MiB. Stop keeps
// the values of keep alive until it has read the heap, so that the live heap
// includes the result of the work.
func (m *Meter) Stop(b *testing.B, keep ...any) {
	b.Helper()

	close(m.stop)
	m.done.Wait()

	var after runtime.MemStats

	debug.FreeOSMemory()
	runtime.ReadMemStats(&after)

	runtime.KeepAlive(keep)

	b.ReportMetric((float64(after.HeapAlloc)-float64(m.before.HeapAlloc))/mib, "live-MiB")
	b.ReportMetric(float64(m.peak.Load())/mib, "peak-MiB")
	b.ReportMetric(float64(after.TotalAlloc-m.before.TotalAlloc)/mib, "alloc-MiB")
}
