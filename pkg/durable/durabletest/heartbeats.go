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
	// Seed writes one beat of owner at the given time of the store, with
	// the given lease, as an upsert by owner. It is a test hook for ordering
	// cases that cannot wait on the clock of the store.
	Seed func(t *testing.T, owner string, at time.Time, lease time.Duration)
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
		{"a beat records the lease of its owner", testBeatLease},
		{"a lease that is not positive is not valid", testBeatInvalidLease},
		{"the beat that stays valid the longest wins over a later beat", testLongestValidWins},
		{"the long lease wins after the later short lease expires", testShortLeaseExpiresFirst},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

// beatLease is the lease period that mustBeat records.
const beatLease = 30 * time.Second

func mustBeat(t *testing.T, s durable.HeartbeatStore, owner string) {
	t.Helper()

	mustBeatLease(t, s, owner, beatLease)
}

func mustBeatLease(t *testing.T, s durable.HeartbeatStore, owner string, lease time.Duration) {
	t.Helper()

	if err := s.Beat(t.Context(), owner, lease); err != nil {
		t.Fatalf("Beat(%q, %v): %v", owner, lease, err)
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
	if err := s.Beat(t.Context(), "", beatLease); !errors.Is(err, durable.ErrInvalid) {
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

func testBeatLease(t *testing.T, h HeartbeatHarness) {
	t.Helper()

	s := h.Open(t, 0)
	mustBeatLease(t, s, "worker-a", 90*time.Second)

	if beat := mustLastBeat(t, s); beat.Lease != 90*time.Second {
		t.Errorf("LastBeat lease = %v, want %v", beat.Lease, 90*time.Second)
	}

	mustBeatLease(t, s, "worker-a", 5*time.Second)

	if beat := mustLastBeat(t, s); beat.Lease != 5*time.Second {
		t.Errorf("LastBeat lease after a beat with another lease = %v, want %v", beat.Lease, 5*time.Second)
	}

	if beat := mustLastBeat(t, h.Open(t, 0)); beat.Lease != 5*time.Second {
		t.Errorf("LastBeat lease from another handle = %v, want %v", beat.Lease, 5*time.Second)
	}
}

func testBeatInvalidLease(t *testing.T, h HeartbeatHarness) {
	t.Helper()

	s := h.Open(t, 0)

	for _, lease := range []time.Duration{0, -time.Second} {
		if err := s.Beat(t.Context(), "worker-a", lease); !errors.Is(err, durable.ErrInvalid) {
			t.Errorf("Beat with lease %v: error = %v, want ErrInvalid", lease, err)
		}
	}

	if beat := mustLastBeat(t, s); beat.Owner != "" || !beat.At.IsZero() {
		t.Errorf("LastBeat after a failed beat = %+v, want no beat", beat)
	}
}

// testLongestValidWins proves that a worker with a long lease still counts
// when a worker with a short lease beats later: the beat that stays valid the
// longest is the answer.
func testLongestValidWins(t *testing.T, h HeartbeatHarness) {
	t.Helper()

	s := h.Open(t, 0)
	mustBeatLease(t, s, "long", time.Hour)
	pause(t, 5*time.Millisecond)
	mustBeatLease(t, s, "short", time.Second)

	beat := mustLastBeat(t, s)
	if beat.Owner != "long" || beat.Lease != time.Hour {
		t.Errorf("LastBeat = %+v, want the beat of the owner with the lease of 1 hour", beat)
	}
}

// testShortLeaseExpiresFirst proves that the answer is the beat whose time
// plus 2 lease periods is the latest, not the latest beat: an earlier beat
// with a long lease stays the answer when a later beat with a short lease
// would have expired. Seed writes the times, so the case does not wait on
// the clock of the store.
func testShortLeaseExpiresFirst(t *testing.T, h HeartbeatHarness) {
	t.Helper()

	// base is a fixed time of the store, far from the clock of the test.
	base := time.UnixMilli(1_700_000_000_000)

	type beatSeed struct {
		owner string
		at    time.Time
		lease time.Duration
	}

	cases := []struct {
		name      string
		seeds     []beatSeed
		wantOwner string
		wantLease time.Duration
	}{
		{
			// The time plus one lease answers "short", because its beat is
			// later. The time plus 2 lease periods answers "long", because
			// its lease is longer.
			name: "an earlier long lease outlasts a later short lease",
			seeds: []beatSeed{
				{owner: "long", at: base, lease: 2 * time.Second},
				{owner: "short", at: base.Add(2 * time.Second), lease: 500 * time.Millisecond},
			},
			wantOwner: "long",
			wantLease: 2 * time.Second,
		},
		{
			// Both beats have the same time and lease, so the greatest
			// owner breaks the tie under the old and the new ordering. The
			// row writes the two owners again, so it does not depend on the
			// row before it.
			name: "equal times and leases give the greatest owner",
			seeds: []beatSeed{
				{owner: "long", at: base, lease: 2 * time.Second},
				{owner: "short", at: base, lease: 2 * time.Second},
			},
			wantOwner: "short",
			wantLease: 2 * time.Second,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, seed := range tc.seeds {
				h.Seed(t, seed.owner, seed.at, seed.lease)
			}

			beat := mustLastBeat(t, h.Open(t, 0))
			if beat.Owner != tc.wantOwner || beat.Lease != tc.wantLease {
				t.Errorf("LastBeat = %+v, want owner %q with lease %v", beat, tc.wantOwner, tc.wantLease)
			}
		})
	}
}
