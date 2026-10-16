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

	if len(ops) != 1 || ops[0].ID != held.ID || ops[0].Claim.Token != held.Claim.Token {
		t.Fatalf("durable operations after the restore = %+v, want only %s with token %d", ops, held.ID,
			held.Claim.Token)
	}

	// The live holder keeps its claim across the restore.
	if _, err := dstOps.DurableStore().Extend(ctx, held.Claim, time.Minute); err != nil {
		t.Fatalf("extend of the live claim after the restore: %v", err)
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
