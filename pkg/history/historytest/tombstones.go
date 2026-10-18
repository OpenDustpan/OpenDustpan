package historytest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// TombstoneStore is every persistence port that RunTombstones exercises:
// namespaces for the fence, and history.TombstoneLog under test.
type TombstoneStore interface {
	namespace.Store
	history.TombstoneLog
}

// TombstoneFactory returns a fresh, empty store for one subtest.
type TombstoneFactory func(t *testing.T) TombstoneStore

// RunTombstones runs the conformance suite for history.TombstoneLog.
func RunTombstones(t *testing.T, newStore TombstoneFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s TombstoneStore)
		name string
	}{
		{name: "append then get by unit", test: testTombstoneAppendGet},
		{name: "append makes a pending live target first, then each backup target", test: testTombstoneTargets},
		{name: "a second tombstone of one unit is ErrPurged", test: testTombstoneTwice},
		{name: "an invalid tombstone is refused", test: testTombstoneInvalid},
		{name: "a stale fence refuses the append", test: testTombstoneFenced},
		{name: "pending tombstones stay listed until every target is done", test: testTombstonePending},
		{name: "a failure counts the try and keeps the target pending", test: testTombstoneFailure},
		{name: "an unknown target or tombstone is refused", test: testTombstoneUnknown},
		{name: "a requeue sets only the named targets pending again", test: testTombstoneRequeue},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// tombstoneUnit is the unit ID of the tombstones of the cases.
const tombstoneUnit = "ws-one"

// newTombstone returns a valid tombstone of the unit tombstoneUnit, made at
// at.
func newTombstone(at time.Time) history.Tombstone {
	const unitID = tombstoneUnit

	return history.Tombstone{
		At:        at,
		Unit:      unitOf(unitID),
		ID:        history.NewTombstoneID(),
		By:        actor(),
		AuditHash: "4f2a",
		ItemKeys:  []secret.ItemKeyID{secret.NewItemKeyID(), secret.NewItemKeyID()},
		Prefixes:  []string{"opentofu/" + unitID + "/"},
		Targets:   nil,
	}
}

// mustTombstone appends tb with targets and returns the stored record.
func mustTombstone(
	t *testing.T, s TombstoneStore, tb history.Tombstone, targets []string, fence namespace.Fence,
) history.Tombstone {
	t.Helper()

	got, err := s.AppendTombstone(t.Context(), tb, targets, fence)
	if err != nil {
		t.Fatalf("append tombstone %s: %v", tb.ID, err)
	}

	return got
}

// tombstoneOf returns the stored tombstone of the unit tombstoneUnit.
func tombstoneOf(t *testing.T, s TombstoneStore) history.Tombstone {
	t.Helper()

	got, err := s.TombstoneOf(t.Context(), unitOf(tombstoneUnit))
	if err != nil {
		t.Fatalf("tombstone of %s: %v", tombstoneUnit, err)
	}

	return got
}

func testTombstoneAppendGet(t *testing.T, s TombstoneStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	tb := newTombstone(suiteTime())
	mustTombstone(t, s, tb, nil, fence)

	got := tombstoneOf(t, s)
	if got.ID != tb.ID || got.Unit != tb.Unit || got.By != tb.By || !got.At.Equal(tb.At) ||
		got.AuditHash != tb.AuditHash || !slices.Equal(got.ItemKeys, tb.ItemKeys) ||
		!slices.Equal(got.Prefixes, tb.Prefixes) {
		t.Errorf("tombstone of unit = %+v, want %+v", got, tb)
	}

	if _, err := s.TombstoneOf(t.Context(), unitOf("ws-two")); !errors.Is(err, history.ErrTombstoneNotFound) {
		t.Errorf("tombstone of another unit err = %v, want ErrTombstoneNotFound", err)
	}

	listed, err := s.ListTombstones(t.Context(), "", 10)
	if err != nil || len(listed) != 1 || listed[0].ID != tb.ID {
		t.Errorf("listed = %v, %v, want the tombstone", listed, err)
	}
}

func testTombstoneTargets(t *testing.T, s TombstoneStore) {
	t.Helper()
	fence := makeNamespace(t, s)

	got := mustTombstone(t, s, newTombstone(suiteTime()), []string{backupTarget, "s3://bucket/prefix"}, fence)

	names := make([]string, 0, len(got.Targets))

	for _, target := range got.Targets {
		names = append(names, target.Target)

		if target.State != history.ShredPending || target.Attempts != 0 {
			t.Errorf("target %s = %+v, want pending with no try", target.Target, target)
		}
	}

	if want := []string{history.LiveTarget, backupTarget, "s3://bucket/prefix"}; !slices.Equal(names, want) {
		t.Errorf("targets = %v, want %v", names, want)
	}

	if !got.Pending() {
		t.Error("a new tombstone is not pending")
	}
}

func testTombstoneTwice(t *testing.T, s TombstoneStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	first := mustTombstone(t, s, newTombstone(suiteTime()), nil, fence)

	got, err := s.AppendTombstone(t.Context(), newTombstone(suiteTime().Add(time.Minute)), nil, fence)
	if !errors.Is(err, history.ErrPurged) {
		t.Fatalf("second tombstone err = %v, want ErrPurged", err)
	}

	if got.ID != first.ID {
		t.Errorf("second tombstone answers %s, want the first %s", got.ID, first.ID)
	}
}

func testTombstoneInvalid(t *testing.T, s TombstoneStore) {
	t.Helper()
	fence := makeNamespace(t, s)

	cases := []struct {
		edit func(tb *history.Tombstone)
		want error
		name string
	}{
		{name: "no prefix", edit: func(tb *history.Tombstone) { tb.Prefixes = nil }, want: history.ErrInvalidTombstone},
		{name: "no actor", edit: func(tb *history.Tombstone) { tb.By.Subject = "" }, want: history.ErrInvalidTombstone},
		{name: "bad unit", edit: func(tb *history.Tombstone) { tb.Unit.ID = "" }, want: history.ErrInvalidItem},
	}

	for _, tc := range cases {
		tb := newTombstone(suiteTime())
		tc.edit(&tb)

		if _, err := s.AppendTombstone(t.Context(), tb, nil, fence); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	if _, err := s.TombstoneOf(t.Context(), unitOf("ws-one")); !errors.Is(err, history.ErrTombstoneNotFound) {
		t.Errorf("an invalid append left a tombstone: err = %v", err)
	}
}

func testTombstoneFenced(t *testing.T, s TombstoneStore) {
	t.Helper()
	fence := makeNamespace(t, s)

	_, err := s.AppendTombstone(t.Context(), newTombstone(suiteTime()), nil, staleFence(fence))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Errorf("append with a stale fence err = %v, want ErrFenced", err)
	}

	if _, err := s.TombstoneOf(t.Context(), unitOf("ws-one")); !errors.Is(err, history.ErrTombstoneNotFound) {
		t.Errorf("a fenced append left a tombstone: err = %v", err)
	}
}

func testTombstonePending(t *testing.T, s TombstoneStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	tb := mustTombstone(t, s, newTombstone(suiteTime()), []string{backupTarget}, fence)
	first := suiteTime().Add(time.Hour)

	for _, target := range []string{history.LiveTarget, backupTarget} {
		pending, err := s.ListPendingTombstones(t.Context(), "", 10)
		if err != nil || len(pending) != 1 || pending[0].ID != tb.ID {
			t.Fatalf("pending before %s = %v, %v, want the tombstone", target, pending, err)
		}

		if err := s.FinishTombstone(t.Context(), tb.ID, target, first); err != nil {
			t.Fatalf("finish %s: %v", target, err)
		}
	}

	if err := s.FinishTombstone(t.Context(), tb.ID, history.LiveTarget, first.Add(time.Hour)); err != nil {
		t.Fatalf("second finish: %v", err)
	}

	pending, err := s.ListPendingTombstones(t.Context(), "", 10)
	if err != nil || len(pending) != 0 {
		t.Errorf("pending after every target = %v, %v, want none", pending, err)
	}

	got := tombstoneOf(t, s)
	if got.Pending() || !got.Targets[0].DoneAt.Equal(first) {
		t.Errorf("tombstone after every target = %+v, want done at the first time %v", got, first)
	}
}

func testTombstoneFailure(t *testing.T, s TombstoneStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	tb := mustTombstone(t, s, newTombstone(suiteTime()), []string{backupTarget}, fence)
	at := suiteTime().Add(time.Hour)

	for range 2 {
		err := s.NoteTombstoneFailure(
			t.Context(),
			history.TombstoneFailure{At: at, ID: tb.ID, Target: backupTarget, Text: "target does not answer"},
		)
		if err != nil {
			t.Fatalf("note failure: %v", err)
		}
	}

	target := tombstoneOf(t, s).Targets[1]
	if target.State != history.ShredPending || target.Attempts != 2 || target.LastError != "target does not answer" ||
		!target.LastTryAt.Equal(at) {
		t.Errorf("target after two failures = %+v, want pending with 2 tries and the text", target)
	}
}

func testTombstoneUnknown(t *testing.T, s TombstoneStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	tb := mustTombstone(t, s, newTombstone(suiteTime()), nil, fence)

	err := s.FinishTombstone(t.Context(), tb.ID, "dir:/other", suiteTime())
	if !errors.Is(err, history.ErrTargetNotFound) {
		t.Errorf("finish of an unknown target err = %v, want ErrTargetNotFound", err)
	}

	err = s.FinishTombstone(t.Context(), history.NewTombstoneID(), history.LiveTarget, suiteTime())
	if !errors.Is(err, history.ErrTombstoneNotFound) {
		t.Errorf("finish of an unknown tombstone err = %v, want ErrTombstoneNotFound", err)
	}

	err = s.NoteTombstoneFailure(
		t.Context(),
		history.TombstoneFailure{At: suiteTime(), ID: history.NewTombstoneID(), Target: history.LiveTarget, Text: "x"},
	)
	if !errors.Is(err, history.ErrTombstoneNotFound) {
		t.Errorf("note failure of an unknown tombstone err = %v, want ErrTombstoneNotFound", err)
	}
}

// testTombstoneRequeue checks the restore rule: after the swap, only the
// live target and the source target of the restore become pending again,
// so the shred operation deletes the blobs of a purged unit again where a
// restore can bring them back.
func testTombstoneRequeue(t *testing.T, s TombstoneStore) {
	t.Helper()
	fence := makeNamespace(t, s)
	const other = "s3://bucket/prefix"
	tb := mustTombstone(t, s, newTombstone(suiteTime()), []string{backupTarget, other}, fence)

	for _, target := range []string{history.LiveTarget, backupTarget, other} {
		if err := s.FinishTombstone(t.Context(), tb.ID, target, suiteTime().Add(time.Hour)); err != nil {
			t.Fatalf("finish %s: %v", target, err)
		}
	}

	if err := s.RequeueTombstones(t.Context(), []string{history.LiveTarget, backupTarget}); err != nil {
		t.Fatalf("requeue: %v", err)
	}

	want := map[string]history.ShredState{
		history.LiveTarget: history.ShredPending,
		backupTarget:       history.ShredPending,
		other:              history.ShredDone,
	}

	for _, target := range tombstoneOf(t, s).Targets {
		if target.State != want[target.Target] {
			t.Errorf("target %s after a requeue = %s, want %s", target.Target, target.State, want[target.Target])
		}
	}
}
