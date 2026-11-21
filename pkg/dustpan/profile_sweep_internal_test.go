package dustpan

import (
	"bytes"
	"context"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/profile"
)

// sweepRecorder is a profile.Sessions store that records the arguments of its
// Sweep.
type sweepRecorder struct {
	profile.Sessions

	now   time.Time
	limit int
}

func (r *sweepRecorder) Sweep(_ context.Context, now time.Time, limit int) (int, error) {
	r.now, r.limit = now, limit

	return 0, nil
}

// TestSweepProfileSessionsUsesTheServerClock fails when the sweep reads the
// wall clock instead of the clock of the server: the sweep then deletes by a
// time that no test of the server can set.
func TestSweepProfileSessionsUsesTheServerClock(t *testing.T) {
	t.Parallel()

	fixed := time.Date(2031, time.May, 4, 3, 2, 1, 0, time.UTC)
	rec := &sweepRecorder{Sessions: nil, now: time.Time{}, limit: 0}

	s := sweepChainServer(t, &bytes.Buffer{}, nil, nil, nil)
	s.profileSessions = rec
	s.now = func() time.Time { return fixed }

	s.sweepProfileSessionsOnce(t.Context())

	if !rec.now.Equal(fixed) {
		t.Errorf("sweep time = %s, want the clock of the server %s", rec.now, fixed)
	}

	if rec.limit != profileSessionSweepLimit {
		t.Errorf("sweep limit = %d, want %d", rec.limit, profileSessionSweepLimit)
	}
}
