package idempotency_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/idempotency"
)

// TestNewRetention proves the minimum of a retention: 23 hours is refused,
// and 24 hours and 48 hours are accepted.
func TestNewRetention(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		period  time.Duration
		wantErr bool
	}{
		{name: "23 hours", period: 23 * time.Hour, wantErr: true},
		{name: "24 hours", period: 24 * time.Hour, wantErr: false},
		{name: "48 hours", period: 48 * time.Hour, wantErr: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := idempotency.NewRetention(tc.period)
			if tc.wantErr {
				if !errors.Is(err, idempotency.ErrInvalid) {
					t.Fatalf("NewRetention(%s) = %v, want ErrInvalid", tc.period, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("NewRetention(%s) = %v", tc.period, err)
			}
		})
	}
}

// TestRetentionExpired proves the end of the retention: a record of exactly
// the retention age is live, and one millisecond more is expired.
func TestRetentionExpired(t *testing.T) {
	t.Parallel()

	retention, err := idempotency.NewRetention(24 * time.Hour)
	if err != nil {
		t.Fatalf("NewRetention: %v", err)
	}

	created := time.UnixMilli(1_700_000_000_000).UTC()

	tests := []struct {
		name    string
		now     time.Time
		expired bool
	}{
		{name: "just before the end", now: created.Add(24*time.Hour - time.Millisecond), expired: false},
		{name: "exactly 24 hours", now: created.Add(24 * time.Hour), expired: false},
		{name: "24 hours and 1 millisecond", now: created.Add(24*time.Hour + time.Millisecond), expired: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := retention.Expired(created, tc.now)
			if err != nil {
				t.Fatalf("Expired = %v", err)
			}

			if got != tc.expired {
				t.Fatalf("Expired at %v = %t, want %t", tc.now, got, tc.expired)
			}
		})
	}
}

// TestRetentionZeroValue proves that the zero value fails closed: an expiry
// check with it returns ErrInvalid.
func TestRetentionZeroValue(t *testing.T) {
	t.Parallel()

	created := time.UnixMilli(1_700_000_000_000).UTC()

	expired, err := idempotency.Retention{}.Expired(created, created)
	if !errors.Is(err, idempotency.ErrInvalid) {
		t.Fatalf("Expired with the zero value = %t, %v; want ErrInvalid", expired, err)
	}
}
