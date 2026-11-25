package codec_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/store/codec"
	"opendustpan.org/opendustpan/pkg/idempotency"
)

// TestIdempotencyExpired checks the expiry layer of the read: a record past
// its retention counts as absent, and a record at its expiry is still live.
func TestIdempotencyExpired(t *testing.T) {
	t.Parallel()

	retention := idempotency.JobStartRetention()
	created := int64(1_700_000_000_000)
	expiry := codec.IdempotencyExpiryMillis(created, retention)

	if expiry != created+retention.Period().Milliseconds() {
		t.Fatalf("expiry = %d, want %d", expiry, created+retention.Period().Milliseconds())
	}

	tests := []struct {
		name    string
		nowMs   int64
		expired bool
	}{
		{name: "before the expiry", nowMs: expiry - 1, expired: false},
		{name: "at the expiry", nowMs: expiry, expired: false},
		{name: "past the expiry", nowMs: expiry + 1, expired: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := codec.IdempotencyExpired(created, tc.nowMs, retention); got != tc.expired {
				t.Fatalf("expired = %t, want %t", got, tc.expired)
			}
		})
	}
}
