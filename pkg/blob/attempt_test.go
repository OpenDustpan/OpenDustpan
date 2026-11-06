package blob_test

import (
	"context"
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

// fixedClock is a DatabaseClock with a fixed answer.
type fixedClock struct {
	now time.Time
	err error
}

func (c fixedClock) DatabaseNow(context.Context) (time.Time, error) { return c.now, c.err }

// errClockRead is the failure of a read of the clock in the cases.
var errClockRead = errors.New("read of the clock failed")

func TestSweepNow(t *testing.T) {
	t.Parallel()

	db := time.UnixMilli(1_790_000_000_000).UTC()

	tests := []struct {
		name     string
		clock    blob.DatabaseClock
		instance time.Time
		want     error
	}{
		{name: "the clocks agree", clock: fixedClock{now: db, err: nil}, instance: db.Add(time.Second), want: nil},
		{
			name:     "a fast instance at the skew",
			clock:    fixedClock{now: db, err: nil},
			instance: db.Add(blob.AttemptSkew),
			want:     nil,
		},
		{
			name: "an instance that runs fast", clock: fixedClock{now: db, err: nil},
			instance: db.Add(blob.AttemptSkew + time.Second), want: blob.ErrClockSkew,
		},
		{
			name: "an instance that runs slow", clock: fixedClock{now: db, err: nil},
			instance: db.Add(-blob.AttemptSkew - time.Second), want: blob.ErrClockSkew,
		},
		{name: "a failed read", clock: fixedClock{now: db, err: errClockRead}, instance: db, want: blob.ErrClockSkew},
		{name: "no clock", clock: nil, instance: db, want: blob.ErrClockSkew},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := blob.SweepNow(t.Context(), tc.clock, tc.instance)

			switch {
			case tc.want != nil && !errors.Is(err, tc.want):
				t.Errorf("SweepNow = %v, %v; want %v", got, err, tc.want)
			case tc.want == nil && (err != nil || !got.Equal(db)):
				t.Errorf("SweepNow = %v, %v; want the clock of the database %v", got, err, db)
			default:
			}
		})
	}
}

func TestOldEnough(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1_790_000_000_000).UTC()

	tests := []struct {
		name     string
		modified time.Time
		want     bool
	}{
		{name: "older than the grace period", modified: now.Add(-blob.UnnamedGracePeriod), want: true},
		{name: "younger than the grace period", modified: now.Add(-blob.UnnamedGracePeriod + time.Second), want: false},
		{name: "no modification time", modified: time.Time{}, want: false},
	}

	for _, tc := range tests {
		if got := blob.OldEnough(tc.modified, now); got != tc.want {
			t.Errorf("%s: OldEnough = %v, want %v", tc.name, got, tc.want)
		}
	}
}
