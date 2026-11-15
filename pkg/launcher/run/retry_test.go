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

// TestRetryHonorsRetryAfter checks the waits of the retries of a report that
// got a 429 or a 503. A Retry-After inside the cap sets each wait. A longer
// one gives runner.MinLease. A negative one is not honored, so the backoff
// stays. The waits are recorded by the injected sleep; the test never waits
// on the clock.
func TestRetryHonorsRetryAfter(t *testing.T) {
	t.Parallel()

	unavailable := func(after time.Duration) *session.Response {
		r := refusal(503, protocol.ErrorUnavailable)
		r.RetryAfter = after

		return r
	}

	// The config of the tests waits 1ms before the first retry, and the
	// backoff doubles, over the four retries.
	backoff := []time.Duration{time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond, 8 * time.Millisecond}

	cases := []struct {
		name      string
		answer    *session.Response
		wantWaits []time.Duration
	}{
		{
			"429 with no protocol body within the cap",
			&session.Response{Status: 429, Body: []byte("slow down"), RetryAfter: 10 * time.Second},
			[]time.Duration{10 * time.Second, 10 * time.Second, 10 * time.Second, 10 * time.Second},
		},
		{
			"503 above the cap",
			unavailable(time.Minute),
			[]time.Duration{runner.MinLease, runner.MinLease, runner.MinLease, runner.MinLease},
		},
		{
			"503 far above the cap",
			unavailable(24 * time.Hour),
			[]time.Duration{runner.MinLease, runner.MinLease, runner.MinLease, runner.MinLease},
		},
		{
			"503 with a negative Retry-After",
			unavailable(-time.Second),
			backoff,
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

			if len(waits) != len(tc.wantWaits) {
				t.Fatalf("waits = %v, want %v", waits, tc.wantWaits)
			}

			for i, d := range waits {
				if d != tc.wantWaits[i] {
					t.Errorf("wait %d = %s, want %s", i, d, tc.wantWaits[i])
				}
			}
		})
	}
}

// TestReportRetriesKeepTheClaim checks that the renewal loop still renews
// while a retry of the report waits. Each wait blocks until a renewal ran
// after the wait began, so a launcher that stops the renewal before the report
// never ends a wait: the test then runs into the test timeout.
func TestReportRetriesKeepTheClaim(t *testing.T) {
	t.Parallel()

	ticks := make(chan struct{}, 1)

	f := newFake(step("ok", "true"))
	f.finishAnswer = refusal(503, protocol.ErrorUnavailable)
	f.renew = func(int) *session.Response {
		// A tick that nobody reads is dropped: only a renewal that comes
		// after the wait began can end the wait.
		select {
		case ticks <- struct{}{}:
		default:
		}

		return nil
	}

	cfg := config(t, f)
	cfg.Sleep = func(ctx context.Context, _ time.Duration) bool {
		// Drop the ticks of the run steps, so that only a renewal during
		// this wait ends it.
		select {
		case <-ticks:
		default:
		}

		select {
		case <-ticks:
			return true
		case <-ctx.Done():
			return false
		}
	}

	wantExit(t, cfg, launcher.ExitUnreported)
}
