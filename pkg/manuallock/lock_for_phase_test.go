package manuallock_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/manuallock"
)

// TestLockForPhaseTable sends each case of the rule of a job phase straight to
// the pure function that a transaction applies: no store and no clock.
func TestLockForPhaseTable(t *testing.T) {
	t.Parallel()

	at := func(sec int64) time.Time { return time.Unix(1_700_000_000+sec, 0).UTC() }

	lock := func(holder string, since time.Time) manuallock.Lock {
		var l manuallock.Lock

		l.Holder, l.Since = holder, since

		return l
	}

	unit, collection := lock("unit", at(10)), lock("collection", at(20))

	for _, c := range []struct {
		name    string
		locks   []manuallock.Lock
		started time.Time
		want    string
		wantErr error
	}{
		{"no lock", nil, at(5), "", manuallock.ErrNotLocked},
		{"no lock and no start", nil, time.Time{}, "", manuallock.ErrNotLocked},
		{"no start keeps the first lock", []manuallock.Lock{unit, collection}, time.Time{}, "unit", nil},
		{"phase before every lock passes", []manuallock.Lock{unit, collection}, at(5), "", manuallock.ErrNotLocked},
		{"phase at the time of a lock does not pass it", []manuallock.Lock{unit}, at(10), "unit", nil},
		{"phase after the first lock", []manuallock.Lock{unit, collection}, at(15), "unit", nil},
		{"phase before the first lock and after the second", []manuallock.Lock{collection, unit}, at(15), "unit", nil},
		{"phase after every lock", []manuallock.Lock{unit, collection}, at(30), "unit", nil},
	} {
		got, err := manuallock.LockForPhase(c.locks, c.started)
		if !errors.Is(err, c.wantErr) || got.Holder != c.want {
			t.Errorf("%s: LockForPhase = %q, %v, want %q, %v", c.name, got.Holder, err, c.want, c.wantErr)
		}
	}
}
