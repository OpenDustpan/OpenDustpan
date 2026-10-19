package storetest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/idempotency"
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

	src := newStore(t)

	srcOps, ok := src.(DurableHolder)
	if !ok {
		t.Skip("the store keeps no durable operations")
	}

	dst := newStore(t)

	dstOps, ok := dst.(DurableHolder)
	if !ok {
		t.Skip("the store keeps no durable operations")
	}

	ps := portableRestoreStores(t, src, dst)
	ctx := t.Context()

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

// restoreAnswerStatus is the status of the answer that the restore case stores.
const restoreAnswerStatus = 201

// IdempotencyHolder is an optional store capability for the idempotency case
// of the restore suite: the store of replay records on the database of the
// store.
type IdempotencyHolder interface {
	IdempotencyStore() idempotency.Store
}

// testRestoreEmptiesIdempotencyRecords proves that a restore leaves no replay
// record: a record answers for the state at the time of the first request,
// and the restore removed that state.
func testRestoreEmptiesIdempotencyRecords(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	src := newStore(t)
	dst := newStore(t)

	srcRecords, ok := src.(IdempotencyHolder)
	if !ok {
		t.Skip("the store keeps no replay records")
	}

	dstRecords, ok := dst.(IdempotencyHolder)
	if !ok {
		t.Skip("the store keeps no replay records")
	}

	ps := portableRestoreStores(t, src, dst)
	ctx := t.Context()
	ref := idempotency.Ref{
		Principal: "alice", Method: "POST", Path: "/api/dustpan/v1/namespaces/dev/profiles", Key: "restore-key",
	}
	now := time.Now()

	answer := idempotency.Answer{Status: restoreAnswerStatus, Headers: nil, Body: []byte("{}")}

	for name, records := range map[string]idempotency.Store{
		"backup": srcRecords.IdempotencyStore(), "live": dstRecords.IdempotencyStore(),
	} {
		res, err := records.Begin(ctx, ref, []byte("body"), now)
		if err != nil || res.Outcome != idempotency.Started {
			t.Fatalf("begin in the %s store: outcome = %d, err = %v", name, res.Outcome, err)
		}

		if err = records.Complete(ctx, ref, res.Token, answer); err != nil {
			t.Fatalf("complete in the %s store: %v", name, err)
		}
	}

	seedBackupStore(t, ps.src)
	sink, sum, files := exportPortable(t, ps.src)

	if _, err := ps.dst.StagePortable(ctx, sink.read, sum, files); err != nil {
		t.Fatalf("stage portable backup: %v", err)
	}

	if err := ps.dst.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staged backup: %v", err)
	}

	res, err := dstRecords.IdempotencyStore().Begin(ctx, ref, []byte("body"), now)
	if err != nil || res.Outcome != idempotency.Started {
		t.Fatalf("begin after the restore: outcome = %d, err = %v, want Started (no record)", res.Outcome, err)
	}
}
