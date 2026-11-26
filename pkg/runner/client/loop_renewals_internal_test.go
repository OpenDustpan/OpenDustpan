package client

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// noRequestTransport fails the test when the loop sends a request.
type noRequestTransport struct{ t *testing.T }

func (n noRequestTransport) Do(context.Context, *session.Request) (*Answer, error) {
	n.t.Error("the loop sent a request")

	return nil, errNoRequest
}

var errNoRequest = errors.New("no request expected")

// TestRenewalsRefuseALeaseOutOfRange sends a claim with a hostile lease straight
// to renewals, as if poll did not check it. The work must stop with ErrInvalid
// before any ticker or request starts.
func TestRenewalsRefuseALeaseOutOfRange(t *testing.T) {
	t.Parallel()

	cases := map[string]time.Duration{
		"zero":            0,
		"negative":        -time.Minute,
		"below minimum":   runner.MinLease - time.Millisecond,
		"above maximum":   runner.MaxLease + time.Millisecond,
		"far above":       1000 * runner.MaxLease,
		"one millisecond": time.Millisecond,
	}

	for name, lease := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, key, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatalf("key: %v", err)
			}

			l, err := New(Config{
				Server: "https://server.example", Transport: noRequestTransport{t: t},
				RunnerID: "runner-1", Key: key, Release: "1.0.0", Lease: runner.MinLease,
				Wait: 0, Ticker: func(time.Duration) (<-chan time.Time, func()) {
					t.Error("the loop made a ticker")

					return nil, func() {}
				},
				Now: nil, Random: nil, Logger: nil,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			var w protocol.Work
			w.Operation = "plan"
			w.Lease = protocol.FromStd(lease)

			wctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)

			var stopCause error

			l.renewals(wctx, func(error) {}, func(cause error) {
				stopCause = cause

				cancel(cause)
			}, w)

			if !errors.Is(stopCause, ErrInvalid) {
				t.Fatalf("stop cause = %v, want ErrInvalid", stopCause)
			}
		})
	}
}
