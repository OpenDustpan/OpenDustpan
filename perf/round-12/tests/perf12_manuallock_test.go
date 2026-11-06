package dustpan_test

import (
	"net/http"
	"runtime"
	"slices"
	"testing"
	"time"
)

func perfPct(d []time.Duration, p float64) float64 {
	s := slices.Clone(d)
	slices.Sort(s)

	return float64(s[int(float64(len(s)-1)*p)].Microseconds()) / 1000
}

// TestPerf12ManualLockRequest times PUT and DELETE of a unit manual lock through the HTTP route.
func TestPerf12ManualLockRequest(t *testing.T) {
	f := newFixture(t)
	f.createNamespace(t, "acme", installationName)
	workspaceInCollection(t, f)

	const path = "/api/inventory/unit-lock?unit=opentofu:acme/web"

	for range 20 {
		mustDo(t, f, call{method: http.MethodPut, path: path, body: `{"reason":"freeze"}`}, http.StatusOK)
		mustDo(t, f, call{method: http.MethodDelete, path: path}, http.StatusNoContent)
	}

	const n = 500

	var put, del []time.Duration

	var m0, m1 runtime.MemStats

	runtime.GC()
	runtime.ReadMemStats(&m0)

	for range n {
		a := time.Now()
		mustDo(t, f, call{method: http.MethodPut, path: path, body: `{"reason":"freeze"}`}, http.StatusOK)
		b := time.Now()
		mustDo(t, f, call{method: http.MethodDelete, path: path}, http.StatusNoContent)
		put = append(put, b.Sub(a))
		del = append(del, time.Since(b))
	}

	runtime.ReadMemStats(&m1)
	t.Logf("PERF12 manuallock-put n=%d p50=%.3f p95=%.3f p99=%.3f", n, perfPct(put, .5), perfPct(put, .95), perfPct(put, .99))
	t.Logf("PERF12 manuallock-delete n=%d p50=%.3f p95=%.3f p99=%.3f", n, perfPct(del, .5), perfPct(del, .95), perfPct(del, .99))
	t.Logf("PERF12 manuallock-pair allocs/pair=%d bytes/pair=%d", (m1.Mallocs-m0.Mallocs)/n, (m1.TotalAlloc-m0.TotalAlloc)/n)
}
