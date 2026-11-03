package argon2id_test

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/argon2id"
)

func pct(d []time.Duration, p float64) time.Duration {
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[int(float64(len(d)-1)*p)]
}

func TestPerf8SignInCost(t *testing.T) {
	p := localuser.DefaultHashParams()
	t.Logf("params: m=%d KiB t=%d p=%d budget=%d KiB wait=%s", p.MemoryKiB, p.Iterations, p.Parallelism,
		localuser.DefaultHashMemoryBudgetKiB, localuser.HashWait)
	b, _ := argon2id.NewBudget(localuser.DefaultHashMemoryBudgetKiB)
	h, _ := argon2id.New(b)
	ctx := context.Background()
	stored, err := h.Hash(ctx, "correct horse battery", p)
	if err != nil {
		t.Fatal(err)
	}
	// single verify: latency, allocs, bytes
	var ms0, ms1 runtime.MemStats
	const single = 10
	var ds []time.Duration
	runtime.GC()
	runtime.ReadMemStats(&ms0)
	for i := 0; i < single; i++ {
		s := time.Now()
		if _, err := h.Verify(ctx, "correct horse battery", stored, p); err != nil {
			t.Fatal(err)
		}
		ds = append(ds, time.Since(s))
	}
	runtime.ReadMemStats(&ms1)
	t.Logf("single verify n=%d p50=%s p95=%s p99=%s allocs/op=%d bytes/op=%d", single, pct(ds, .5), pct(ds, .95), pct(ds, .99),
		(ms1.Mallocs-ms0.Mallocs)/single, (ms1.TotalAlloc-ms0.TotalAlloc)/single)

	for _, conc := range []int{1, 2, 4, 8, 16, 32, 64} {
		var mu sync.Mutex
		var lat []time.Duration
		var busy, ok atomic.Int64
		var peakUsed, peakHeap, peakSys atomic.Uint64
		stop := make(chan struct{})
		var sw sync.WaitGroup
		sw.Add(1)
		go func() {
			defer sw.Done()
			var m runtime.MemStats
			for {
				select {
				case <-stop:
					return
				case <-time.After(2 * time.Millisecond):
				}
				if u := uint64(b.InUse()); u > peakUsed.Load() {
					peakUsed.Store(u)
				}
				runtime.ReadMemStats(&m)
				if m.HeapInuse > peakHeap.Load() {
					peakHeap.Store(m.HeapInuse)
				}
				if m.Sys > peakSys.Load() {
					peakSys.Store(m.Sys)
				}
			}
		}()
		runtime.GC()
		var wg sync.WaitGroup
		start := time.Now()
		for i := 0; i < conc; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s := time.Now()
				_, err := h.Verify(ctx, "correct horse battery", stored, p)
				d := time.Since(s)
				mu.Lock()
				lat = append(lat, d)
				mu.Unlock()
				if err != nil {
					busy.Add(1)
				} else {
					ok.Add(1)
				}
			}()
		}
		wg.Wait()
		wall := time.Since(start)
		close(stop)
		sw.Wait()
		t.Logf("concurrent=%d ok=%d busy=%d wall=%s p50=%s p95=%s p99=%s peakBudgetKiB=%d (cap %d) peakHeapInuseMiB=%.1f peakSysMiB=%.1f",
			conc, ok.Load(), busy.Load(), wall, pct(lat, .5), pct(lat, .95), pct(lat, .99), peakUsed.Load(),
			localuser.DefaultHashMemoryBudgetKiB, float64(peakHeap.Load())/1048576, float64(peakSys.Load())/1048576)
		if peakUsed.Load() > localuser.DefaultHashMemoryBudgetKiB {
			t.Errorf("budget exceeded: %d", peakUsed.Load())
		}
	}
	_ = fmt.Sprint
}
