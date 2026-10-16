package storetest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
)

// DurableHolder is an optional store capability for the control table case
// of the restore suite: the store of durable operations on the database of
// the store.
type DurableHolder interface {
	DurableStore() durable.Store
}

// durableRestoreKind is the operation kind of the control table case.
const durableRestoreKind durable.Kind = "restore-kind"

// testRestoreKeepsDurableOperations proves that a restore keeps the durable
// operations of the live installation and brings back none from the backup.
// The claim tokens of an operation only grow: a restore that brought back an
// older token would give a new claim the token of a live holder, and the
// writes of that holder would pass the fence.
func testRestoreKeepsDurableOperations(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	ps := portableRestoreStores(t, newStore(t), newStore(t))
	ctx := t.Context()

	srcOps, srcOK := ps.src.(DurableHolder)
	dstOps, dstOK := ps.dst.(DurableHolder)

	if !srcOK || !dstOK {
		t.Skip("the store keeps no durable operations")
	}

	mustStartDurable(t, srcOps.DurableStore(), "from-backup")

	live := dstOps.DurableStore()
	mustStartDurable(t, live, "live")
	first := mustClaimDurable(t, live)

	if err := live.Release(ctx, first.Claim); err != nil {
		t.Fatalf("release: %v", err)
	}

	held := mustClaimDurable(t, live)
	want := []durable.Operation{held}

	// A store that also keeps schedules: the backup holds a schedule that is
	// due, and the live schedule has claimed its tick. The operation of that
	// tick is live too.
	schedules, hasSchedules := scheduleStores(srcOps.DurableStore(), live)
	if hasSchedules {
		mustEnsureSchedule(t, schedules.src)
		mustEnsureSchedule(t, schedules.dst)

		tick, err := schedules.dst.ClaimTick(ctx, restoreSchedule())
		if err != nil {
			t.Fatalf("claim the live tick: %v", err)
		}

		want = append(want, tick.Operation)
	}

	seedBackupStore(t, ps.src)
	sink, sum, files := exportPortable(t, ps.src)

	if _, err := ps.dst.StagePortable(ctx, sink.read, sum, files); err != nil {
		t.Fatalf("stage portable backup: %v", err)
	}

	if err := ps.dst.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staged backup: %v", err)
	}

	ops, err := dstOps.DurableStore().List(ctx, durable.Filter{Kind: "", States: nil, Limit: 0})
	if err != nil {
		t.Fatalf("list durable operations: %v", err)
	}

	checkKeptOperations(t, ops, want)

	// The live holder keeps its claim across the restore.
	if _, err := dstOps.DurableStore().Extend(ctx, held.Claim, time.Minute); err != nil {
		t.Fatalf("extend of the live claim after the restore: %v", err)
	}

	// The live schedule keeps its next run: the tick that it claimed does
	// not fire again.
	if hasSchedules {
		if _, err := schedules.dst.ClaimTick(ctx, restoreSchedule()); !errors.Is(err, durable.ErrNoWork) {
			t.Fatalf("claim of a tick after the restore: error = %v, want ErrNoWork", err)
		}
	}
}

// checkKeptOperations fails t unless ops holds exactly the operations of
// want, each with the claim token that it had before the restore.
func checkKeptOperations(t *testing.T, ops, want []durable.Operation) {
	t.Helper()

	tokens := make(map[durable.ID]int64, len(ops))
	for i := range ops {
		tokens[ops[i].ID] = ops[i].Claim.Token
	}

	if len(ops) != len(want) {
		t.Fatalf("durable operations after the restore = %+v, want %d live operations", ops, len(want))
	}

	for i := range want {
		token, ok := tokens[want[i].ID]
		if !ok || token != want[i].Claim.Token {
			t.Fatalf("durable operations after the restore = %+v, want %s with token %d", ops, want[i].ID,
				want[i].Claim.Token)
		}
	}
}

// schedulePair holds the schedule stores of the source and the destination
// of a restore.
type schedulePair struct {
	src durable.ScheduleStore
	dst durable.ScheduleStore
}

// scheduleStores returns src and dst as schedule stores, and false when a
// store keeps no schedules.
func scheduleStores(src, dst durable.Store) (schedulePair, bool) {
	srcSchedules, srcOK := src.(durable.ScheduleStore)
	dstSchedules, dstOK := dst.(durable.ScheduleStore)

	if !srcOK || !dstOK {
		return schedulePair{src: nil, dst: nil}, false
	}

	return schedulePair{src: srcSchedules, dst: dstSchedules}, true
}

// restoreSchedule is the schedule of the control table case.
func restoreSchedule() durable.ScheduleSpec {
	return durable.ScheduleSpec{
		Name:     "restore-sweep",
		Kind:     durableRestoreKind,
		Version:  0,
		Interval: time.Hour,
		Input:    nil,
	}
}

// mustEnsureSchedule creates the schedule of the control table case in s.
func mustEnsureSchedule(t *testing.T, s durable.ScheduleStore) {
	t.Helper()

	if _, err := s.EnsureSchedule(t.Context(), restoreSchedule()); err != nil {
		t.Fatalf("ensure schedule: %v", err)
	}
}

// mustStartDurable starts an operation with key in s.
func mustStartDurable(t *testing.T, s durable.Store, key string) {
	t.Helper()

	req := durable.StartRequest{Key: key, Kind: durableRestoreKind, Version: 0, Input: nil}
	if _, _, err := s.Start(t.Context(), req); err != nil {
		t.Fatalf("start durable operation %q: %v", key, err)
	}
}

// mustClaimDurable claims one operation of s with a long lease.
func mustClaimDurable(t *testing.T, s durable.Store) durable.Operation {
	t.Helper()

	op, err := s.Claim(t.Context(), durable.ClaimRequest{
		Owner: "instance-a", Kinds: []durable.Kind{durableRestoreKind}, MaxVersions: nil, Lease: time.Hour,
	})
	if errors.Is(err, durable.ErrNoWork) || err != nil {
		t.Fatalf("claim durable operation: %v", err)
	}

	return op
}
