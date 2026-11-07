package run_test

import (
	"context"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher"
	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// TestRetryHonorsRetryAfter checks that the retry of a call that got a 429
// or a 503 waits at least the Retry-After of the answer, even when the
// backoff wait is shorter. The waits are recorded by the injected sleep;
// the test never waits on the clock.
func TestRetryHonorsRetryAfter(t *testing.T) {
	t.Parallel()

	const retryWait = time.Minute

	unavailable := refusal(503, protocol.ErrorUnavailable)
	unavailable.RetryAfter = retryWait

	cases := []struct {
		name   string
		answer *session.Response
	}{
		{"429 with no protocol body", &session.Response{Status: 429, Body: []byte("slow down"), RetryAfter: retryWait}},
		{"503", unavailable},
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
				if d < retryWait {
					t.Errorf("wait %d = %s, want at least the Retry-After of %s", i, d, retryWait)
				}
			}
		})
	}
}
