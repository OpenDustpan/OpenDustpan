package server_test

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

func TestPerf12PasswordSignIn(t *testing.T) {
	f := newAccessFixture(t)

	var created struct {
		PrincipalID string `json:"principal"`
		SetUpCode   string `json:"setUpCode"`
	}

	f.asFresh(t, "admin", apiCall{
		method: http.MethodPost, path: "/api/dustpan/v1/local-users",
		body: `{"userName":"carol","displayName":"Carol","reason":"new team member"}`, want: http.StatusCreated,
	}, &created)

	redeemed := publicPost(t, f, "/api/dustpan/v1/local-users/redeem",
		`{"oneTimeCode":"`+created.SetUpCode+`","password":"`+routePassword+`"}`)
	if redeemed.Code != http.StatusNoContent {
		t.Fatalf("redeem = %d", redeemed.Code)
	}

	body := `{"userName":"carol","password":"` + routePassword + `"}`
	for range 3 {
		publicPost(t, f, "/api/dustpan/v1/sign-in/password", body)
	}

	const n = 100

	var d []time.Duration

	var m0, m1 runtime.MemStats

	runtime.GC()
	runtime.ReadMemStats(&m0)

	for range n {
		a := time.Now()
		rec := publicPost(t, f, "/api/dustpan/v1/sign-in/password", body)
		d = append(d, time.Since(a))

		if rec.Code != http.StatusNoContent {
			t.Fatalf("sign in = %d: %s", rec.Code, rec.Body)
		}
	}

	runtime.ReadMemStats(&m1)
	t.Logf("PERF12 password n=%d p50=%.2f p95=%.2f p99=%.2f allocs/op=%d bytes/op=%d", n,
		perfPct(d, .5), perfPct(d, .95), perfPct(d, .99), (m1.Mallocs-m0.Mallocs)/n, (m1.TotalAlloc-m0.TotalAlloc)/n)
}

func TestPerf12OIDCCallback(t *testing.T) {
	e := newSignInEnv(t, true)

	for range 5 {
		e.signIn(t)
	}

	const n = 300

	var d []time.Duration

	var allocs, bytes uint64

	for range n {
		binding, authURL := e.start(t, "/console/tokens")
		q := e.authorize(t, authURL)

		var m0, m1 runtime.MemStats

		runtime.ReadMemStats(&m0)

		a := time.Now()
		rec := e.callback(t, q, binding)
		d = append(d, time.Since(a))

		runtime.ReadMemStats(&m1)

		allocs += m1.Mallocs - m0.Mallocs
		bytes += m1.TotalAlloc - m0.TotalAlloc

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("callback = %d: %s", rec.Code, rec.Body)
		}
	}

	t.Logf("PERF12 oidc-callback n=%d p50=%.3f p95=%.3f p99=%.3f allocs/op=%d bytes/op=%d", n,
		perfPct(d, .5), perfPct(d, .95), perfPct(d, .99), allocs/n, bytes/n)
}
