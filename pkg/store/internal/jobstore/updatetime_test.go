package jobstore_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
)

// TestNextUpdateAt proves that the update time of a write is strictly after
// the stored update time of its row: a clock that stands still or moves back
// still moves the update time.
func TestNextUpdateAt(t *testing.T) {
	t.Parallel()

	base := time.UnixMilli(1_700_000_000_000).UTC()

	for name, tc := range map[string]struct {
		now    time.Time
		stored time.Time
		want   time.Time
	}{
		"now after the stored time": {
			now: base.Add(2 * time.Millisecond), stored: base, want: base.Add(2 * time.Millisecond),
		},
		"now equal to the stored time": {
			now: base, stored: base, want: base.Add(time.Millisecond),
		},
		"now in the millisecond of the stored time": {
			now: base.Add(500 * time.Microsecond), stored: base, want: base.Add(time.Millisecond),
		},
		"now before the stored time": {
			now: base.Add(-2 * time.Millisecond), stored: base, want: base.Add(time.Millisecond),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := jobstore.NextJobUpdateAt(tc.now, tc.stored); !got.Equal(tc.want) {
				t.Fatalf("NextJobUpdateAt(%v, %v) = %v, want %v", tc.now, tc.stored, got, tc.want)
			}
		})
	}
}
