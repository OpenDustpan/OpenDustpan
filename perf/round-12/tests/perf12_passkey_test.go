package server_test

import (
	"net/http"
	"runtime"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/webauthntest"
)

// One begin budget is 10 per person; spread the ceremonies over people and fixtures.
func TestPerf12PasskeyRegister(t *testing.T) {
	var begin, finish []time.Duration

	var ab, af, bb, bf, n uint64

	for range 8 {
		f := newAccessFixture(t)

		for _, who := range []string{"alice", "olga", "ann", "bob"} {
			c := f.freshPasskeyClient(t, who)

			for range 9 {
				var m0, m1, m3, m4 runtime.MemStats

				runtime.ReadMemStats(&m0)

				a := time.Now()
				rec := c.do(http.MethodPost, beginTarget, nil)
				begin = append(begin, time.Since(a))

				runtime.ReadMemStats(&m1)
				expectPasskeyStatus(t, rec, http.StatusOK)

				challenge, err := webauthntest.ChallengeOf(rec.Body.Bytes())
				if err != nil {
					t.Fatal(err)
				}

				body := finishBody(t, "bench", c.register(challenge))

				runtime.ReadMemStats(&m3)

				a = time.Now()
				rec = c.do(http.MethodPost, finishTarget, body)
				finish = append(finish, time.Since(a))

				runtime.ReadMemStats(&m4)
				expectPasskeyStatus(t, rec, http.StatusCreated)

				ab += m1.Mallocs - m0.Mallocs
				bb += m1.TotalAlloc - m0.TotalAlloc
				af += m4.Mallocs - m3.Mallocs
				bf += m4.TotalAlloc - m3.TotalAlloc
				n++
			}
		}
	}

	t.Logf("PERF12 passkey-begin n=%d p50=%.3f p95=%.3f p99=%.3f allocs/op=%d bytes/op=%d", n,
		perfPct(begin, .5), perfPct(begin, .95), perfPct(begin, .99), ab/n, bb/n)
	t.Logf("PERF12 passkey-finish n=%d p50=%.3f p95=%.3f p99=%.3f allocs/op=%d bytes/op=%d", n,
		perfPct(finish, .5), perfPct(finish, .95), perfPct(finish, .99), af/n, bf/n)
}
