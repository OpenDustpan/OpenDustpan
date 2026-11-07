package run_test

import (
	"context"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher"
	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// TestRetryHonorsRetryAfter checks that the retry of a call that got a 429
// or a 503 waits for the Retry-After of the answer, up to runner.MinLease. A
// longer Retry-After gives the cap. The waits are recorded by the injected
// sleep; the test never waits on the clock.
func TestRetryHonorsRetryAfter(t *testing.T) {
	t.Parallel()

	unavailable := func(after time.Duration) *session.Response {
		r := refusal(503, protocol.ErrorUnavailable)
		r.RetryAfter = after

		return r
	}

	cases := []struct {
		name     string
		answer   *session.Response
		wantWait time.Duration
	}{
		{
			"429 with no protocol body within the cap",
			&session.Response{Status: 429, Body: []byte("slow down"), RetryAfter: 10 * time.Second},
			10 * time.Second,
		},
		{
			"503 above the cap",
			unavailable(time.Minute),
			runner.MinLease,
		},
		{
			"503 far above the cap",
			unavailable(24 * time.Hour),
			runner.MinLease,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(step("ok", "true"))
			f.finishAnswer = tc.answer

			cfg := config(t, f)

			var waits []time.Duration

			cfg.Sleep = func(_ context.Context, d time.Duration) bool {
				waits = append(waits, d)

				return true
			}

			wantExit(t, cfg, launcher.ExitUnreported)

			if len(waits) == 0 {
				t.Fatal("the finish was not retried")
			}

			for i, d := range waits {
				if d != tc.wantWait {
					t.Errorf("wait %d = %s, want %s", i, d, tc.wantWait)
				}
			}
		})
	}
}
