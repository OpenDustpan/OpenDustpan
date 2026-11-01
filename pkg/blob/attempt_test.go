package blob_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
)

func TestCheckAttempt(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1_790_000_000_000).UTC()

	tests := []struct {
		name    string
		started time.Time
		want    error
	}{
		{name: "a fresh attempt", started: now.Add(-time.Second), want: nil},
		{name: "an attempt at the deadline", started: now.Add(-blob.AttemptDeadline), want: nil},
		{
			name:    "an attempt just past the deadline",
			started: now.Add(-blob.AttemptDeadline - time.Millisecond),
			want:    blob.ErrAttemptExpired,
		},
		{
			name:    "an attempt older than the grace period",
			started: now.Add(-2 * time.Hour),
			want:    blob.ErrAttemptExpired,
		},
		{name: "an attempt ahead by the skew", started: now.Add(blob.AttemptSkew), want: nil},
		{
			name:    "an attempt ahead by more than the skew",
			started: now.Add(blob.AttemptSkew + time.Millisecond),
			want:    blob.ErrAttemptExpired,
		},
		{name: "an attempt with no start time", started: time.Time{}, want: blob.ErrAttemptExpired},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := blob.CheckAttempt(tc.started, now); !errors.Is(err, tc.want) || (tc.want == nil) != (err == nil) {
				t.Errorf("CheckAttempt = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestUnnamedGracePeriodCoversTheAttempt shows that a sweep never deletes the
// blob of an attempt that can still commit: the grace period is longer than
// the deadline, the skew and a lease, with a margin for the clocks.
func TestUnnamedGracePeriodCoversTheAttempt(t *testing.T) {
	t.Parallel()

	if blob.UnnamedMargin < 10*time.Minute {
		t.Errorf("UnnamedMargin = %s, want at least 10m", blob.UnnamedMargin)
	}

	if blob.UnnamedGracePeriod <= blob.AttemptDeadline+blob.AttemptSkew {
		t.Errorf("UnnamedGracePeriod = %s, want more than the deadline and the skew", blob.UnnamedGracePeriod)
	}
}
