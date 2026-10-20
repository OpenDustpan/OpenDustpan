package durabletest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
)

// HeartbeatHarness is one fresh, empty database for one heartbeat case.
type HeartbeatHarness struct {
	// Open returns a new handle on the database of the harness, with the
	// clock skew of its instance, as Harness.Open does.
	Open func(t *testing.T, skew time.Duration) durable.HeartbeatStore
}

// HeartbeatFactory returns a fresh heartbeat harness for one case.
type HeartbeatFactory func(t *testing.T) HeartbeatHarness

// beatTolerance is the largest difference allowed between a time of the
// store and the time of the test. It is far below the clock skew of the
// clock case, and far above the time that a loaded runner needs for a call.
const beatTolerance = time.Minute

// RunHeartbeats executes the conformance suite for durable.HeartbeatStore
// against harnesses from newHarness.
func RunHeartbeats(t *testing.T, newHarness HeartbeatFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h HeartbeatHarness)
	}{
		{"a store with no beat answers a zero time and sets the store time", testNoBeat},
		{"a beat is the latest beat", testBeatThenLast},
		{"the latest of two owners wins", testLatestOwnerWins},
		{"a beat of the same owner moves its time", testSameOwnerRenews},
		{"an empty owner is not valid", testBeatEmptyOwner},
		{"an instance clock ahead or behind cannot move a beat", testBeatClockSkew},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

func mustBeat(t *testing.T, s durable.HeartbeatStore, owner string) {
	t.Helper()

	if err := s.Beat(t.Context(), owner); err != nil {
		t.Fatalf("Beat(%q): %v", owner, err)
	}
}

func mustLastBeat(t *testing.T, s durable.HeartbeatStore) durable.Beat {
	t.Helper()

	beat, err := s.LastBeat(t.Context())
	if err != nil {
		t.Fatalf("LastBeat: %v", err)
	}

	return beat
}

// checkNear fails when got is not within beatTolerance of the time of the
// test.
func checkNear(t *testing.T, what string, got time.Time) {
	t.Helper()

	if diff := time.Since(got); diff > beatTolerance || diff < -beatTolerance {
		t.Errorf("%s = %v, want a time within %v of the clock of the store", what, got, beatTolerance)
	}
}

func testNoBeat(t *testing.T, h HeartbeatHarness) {
	t.Helper()

	beat := mustLastBeat(t, h.Open(t, 0))
	if beat.Owner != "" || !beat.At.IsZero() {
		t.Errorf("LastBeat of an empty store = %+v, want no owner and a zero time", beat)
	}

	if beat.Now.IsZero() {
		t.Error("LastBeat of an empty store: Now is zero, want the clock of the store")
	}

	checkNear(t, "Now", beat.Now)
}

func testBeatThenLast(t *testing.T, h HeartbeatHarness) {
	t.Helper()

	s := h.Open(t, 0)
	mustBeat(t, s, "worker-a")

	beat := mustLastBeat(t, s)
	if beat.Owner != "worker-a" {
		t.Errorf("LastBeat owner = %q, want %q", beat.Owner, "worker-a")
	}

	checkNear(t, "At", beat.At)

	if beat.Now.Before(beat.At) {
		t.Errorf("LastBeat: Now %v is before At %v, want Now at or after At", beat.Now, beat.At)
	}
}

func testLatestOwnerWins(t *testing.T, h HeartbeatHarness) {
	t.Helper()

	s, other := h.Open(t, 0), h.Open(t, 0)
	mustBeat(t, s, "worker-a")
	pause(t, 5*time.Millisecond)
	mustBeat(t, other, "worker-b")

	if beat := mustLastBeat(t, s); beat.Owner != "worker-b" {
		t.Errorf("LastBeat owner after b = %q, want %q", beat.Owner, "worker-b")
	}

	pause(t, 5*time.Millisecond)
	mustBeat(t, other, "worker-a")

	if beat := mustLastBeat(t, other); beat.Owner != "worker-a" {
		t.Errorf("LastBeat owner after a again = %q, want %q", beat.Owner, "worker-a")
	}
}

func testSameOwnerRenews(t *testing.T, h HeartbeatHarness) {
	t.Helper()

	s := h.Open(t, 0)
	mustBeat(t, s, "worker-a")

	first := mustLastBeat(t, s)

	pause(t, 5*time.Millisecond)
	mustBeat(t, s, "worker-a")

	second := mustLastBeat(t, s)
	if second.Owner != "worker-a" || !second.At.After(first.At) {
		t.Errorf("LastBeat after a second beat = %+v, want the time to move past %v", second, first.At)
	}
}

func testBeatEmptyOwner(t *testing.T, h HeartbeatHarness) {
	t.Helper()

	s := h.Open(t, 0)
	if err := s.Beat(t.Context(), ""); !errors.Is(err, durable.ErrInvalid) {
		t.Errorf("Beat with an empty owner: error = %v, want ErrInvalid", err)
	}

	if beat := mustLastBeat(t, s); beat.Owner != "" || !beat.At.IsZero() {
		t.Errorf("LastBeat after a failed beat = %+v, want no beat", beat)
	}
}

func testBeatClockSkew(t *testing.T, h HeartbeatHarness) {
	t.Helper()

	ahead, behind := h.Open(t, instanceSkew), h.Open(t, -instanceSkew)
	mustBeat(t, ahead, "worker-a")

	for name, s := range map[string]durable.HeartbeatStore{"ahead": ahead, "behind": behind} {
		beat := mustLastBeat(t, s)
		if beat.Owner != "worker-a" {
			t.Errorf("LastBeat owner from an instance %s = %q, want %q", name, beat.Owner, "worker-a")
		}

		checkNear(t, "At from an instance "+name, beat.At)
		checkNear(t, "Now from an instance "+name, beat.Now)
	}
}
