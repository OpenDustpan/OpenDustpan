package storetest_test

import (
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestSweepReleaseOnce checks that a sweep release gives the lock back once.
// A later call does nothing. A cleanup that runs after an explicit release,
// or alone when a test stops early, calls the same wrapper, so the count holds
// for each of those paths.
func TestSweepReleaseOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		calls int
		want  int32
	}{
		{name: "cleanup only, no explicit release", calls: 1, want: 1},
		{name: "explicit release then cleanup", calls: 2, want: 1},
		{name: "many calls", calls: 5, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var released atomic.Int32

			release := storetest.SweepReleaseOnce(func() { released.Add(1) })

			for range tt.calls {
				release()
			}

			if got := released.Load(); got != tt.want {
				t.Fatalf("release ran %d times after %d calls, want %d", got, tt.calls, tt.want)
			}
		})
	}
}
