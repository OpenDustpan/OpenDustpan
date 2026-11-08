package jobtest

import (
	"testing"
	"time"
)

// testClock is the clock of one case of the sweep suite. The clock holds one
// instant: the time of the clock of the database when the case makes it. The
// sweep refuses a run when its instance clock and the clock of the database do
// not agree, so the case starts from the clock of the database and reads no
// wall clock.
type testClock struct {
	now time.Time
}

// newTestClock returns a clock at the time of the clock of the database of h.
func newTestClock(t *testing.T, h ArtifactSweepHarness) *testClock {
	t.Helper()

	now, err := h.Clock.DatabaseNow(t.Context())
	if err != nil {
		t.Fatalf("read the clock of the database: %v", err)
	}

	return &testClock{now: now}
}

// Now returns the instant of the clock.
func (c *testClock) Now() time.Time { return c.now }
