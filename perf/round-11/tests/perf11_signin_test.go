package server_test

import (
	"net/http"
	"runtime"
	"slices"
	"testing"
	"time"
)

func perf11Report(t *testing.T, name string, d []time.Duration, m0, m1 *runtime.MemStats) {
	t.Helper()
	s := slices.Clone(d)
	slices.Sort(s)
	at := func(q float64) float64 {
		return float64(s[min(len(s)-1, int(q*float64(len(s))))]) / float64(time.Millisecond)
	}
	n := uint64(len(d))
	t.Logf("PERF11 %s n=%d p50=%.3fms p95=%.3fms p99=%.3fms allocs/op=%d bytes/op=%d heapInuseMB=%.1f",
		name, len(d), at(0.5), at(0.95), at(0.99), (m1.Mallocs-m0.Mallocs)/n, (m1.TotalAlloc-m0.TotalAlloc)/n,
		float64(m1.HeapInuse)/1048576)
}

// TestPerf11OIDC times the OIDC sign-in: start, the test provider, and the callback alone.
func TestPerf11OIDC(t *testing.T) {
	e := newSignInEnv(t, true)
	for range 10 {
		e.signIn(t)
	}
	var st, cb []time.Duration
	var m0, m1, m2, m3 runtime.MemStats
	var cbAllocs, cbBytes uint64
	_ = m2
	_ = m3
	runtime.GC()
	runtime.ReadMemStats(&m0)
	for range 300 {
		t0 := time.Now()
		binding, authURL := e.start(t, "/console/tokens")
		st = append(st, time.Since(t0))
		q := e.authorize(t, authURL)
		var a, b runtime.MemStats
		runtime.ReadMemStats(&a)
		t0 = time.Now()
		rec := e.callback(t, q, binding)
		cb = append(cb, time.Since(t0))
		runtime.ReadMemStats(&b)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("callback = %d: %s", rec.Code, rec.Body.String())
		}
		cbAllocs += b.Mallocs - a.Mallocs
		cbBytes += b.TotalAlloc - a.TotalAlloc
	}
	runtime.ReadMemStats(&m1)
	perf11Report(t, "oidc-start", st, &m0, &m0)
	perf11Report(t, "oidc-callback", cb, &m0, &m0)
	t.Logf("PERF11 oidc-callback allocs/op=%d bytes/op=%d (callback only, process-wide counters)", cbAllocs/300, cbBytes/300)
	t.Logf("PERF11 oidc-whole-flow allocs/op=%d bytes/op=%d heapInuseMB=%.1f", (m1.Mallocs-m0.Mallocs)/300, (m1.TotalAlloc-m0.TotalAlloc)/300, float64(m1.HeapInuse)/1048576)
}

// TestPerf11Password times the local password sign-in.
func TestPerf11Password(t *testing.T) {
	f := newAccessFixture(t)
	var created struct {
		SetUpCode string `json:"setUpCode"`
	}
	f.asFresh(t, "admin", apiCall{
		method: http.MethodPost, path: "/api/dustpan/v1/local-users",
		body: `{"userName":"carol","displayName":"Carol","reason":"new team member"}`, want: http.StatusCreated,
	}, &created)
	if r := publicPost(t, f, "/api/dustpan/v1/local-users/redeem",
		`{"oneTimeCode":"`+created.SetUpCode+`","password":"`+routePassword+`"}`); r.Code != http.StatusNoContent {
		t.Fatalf("redeem = %d: %s", r.Code, r.Body)
	}
	body := `{"userName":"carol","password":"` + routePassword + `"}`
	for range 5 {
		publicPost(t, f, "/api/dustpan/v1/sign-in/password", body)
	}
	var d []time.Duration
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	for range 100 {
		t0 := time.Now()
		rec := publicPost(t, f, "/api/dustpan/v1/sign-in/password", body)
		d = append(d, time.Since(t0))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("sign in = %d: %s", rec.Code, rec.Body)
		}
	}
	runtime.ReadMemStats(&m1)
	perf11Report(t, "password-signin", d, &m0, &m1)
}
