package jobtest

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
)

// testClock is the clock of one case of the sweep suite. It reads the clock of
// the database of the harness on each call. The sweep refuses a run when its
// instance clock and the clock of the database do not agree, so the case reads
// the clock of the database and no wall clock.
type testClock struct {
	t     *testing.T
	clock blob.DatabaseClock
}

// newTestClock returns a clock that reads the clock of the database of h. It
// reads the clock once and fails the case when the read fails, so that a broken
// harness stops the case at the start.
func newTestClock(t *testing.T, h ArtifactSweepHarness) *testClock {
	t.Helper()

	if _, err := h.Clock.DatabaseNow(t.Context()); err != nil {
		t.Fatalf("read the clock of the database: %v", err)
	}

	return &testClock{t: t, clock: h.Clock}
}

// Now returns the time of the clock of the database. A read that fails reports
// the error on the test and returns the zero time: the sweep may call Now from
// a goroutine that is not the test goroutine, and a sweep refuses a zero time.
func (c *testClock) Now() time.Time {
	now, err := c.clock.DatabaseNow(c.t.Context())
	if err != nil {
		c.t.Errorf("read the clock of the database: %v", err)

		return time.Time{}
	}

	return now
}
