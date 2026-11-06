package dustpan_test

import (
	"runtime"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/manuallock"
)

// TestPerf12ManualLockService times the service Lock and Unlock through the one authorizer chain (full server).
func TestPerf12ManualLockService(t *testing.T) {
	srv, _ := lockServer(t)
	locks := srv.ManualLocks()
	admin := lockAdmin()
	target := manuallock.UnitTarget(lockUnit("acme"))

	for range 20 {
		if _, err := locks.Lock(t.Context(), admin, target, "freeze", lockAudit()); err != nil {
			t.Fatal(err)
		}

		if err := locks.Unlock(t.Context(), admin, target, lockAudit()); err != nil {
			t.Fatal(err)
		}
	}

	const n = 2000

	var lk, ul []time.Duration

	var m0, m1 runtime.MemStats

	runtime.GC()
	runtime.ReadMemStats(&m0)

	for range n {
		a := time.Now()

		if _, err := locks.Lock(t.Context(), admin, target, "freeze", lockAudit()); err != nil {
			t.Fatal(err)
		}

		b := time.Now()

		if err := locks.Unlock(t.Context(), admin, target, lockAudit()); err != nil {
			t.Fatal(err)
		}

		lk = append(lk, b.Sub(a))
		ul = append(ul, time.Since(b))
	}

	runtime.ReadMemStats(&m1)
	t.Logf("PERF12 manuallock-svc-lock n=%d p50=%.3f p95=%.3f p99=%.3f", n, perfPct(lk, .5), perfPct(lk, .95), perfPct(lk, .99))
	t.Logf("PERF12 manuallock-svc-unlock n=%d p50=%.3f p95=%.3f p99=%.3f", n, perfPct(ul, .5), perfPct(ul, .95), perfPct(ul, .99))
	t.Logf("PERF12 manuallock-svc-pair allocs/pair=%d bytes/pair=%d", (m1.Mallocs-m0.Mallocs)/n, (m1.TotalAlloc-m0.TotalAlloc)/n)
}
