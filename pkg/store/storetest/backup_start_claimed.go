package storetest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
)

// startClaimOwner is the owner of the claim that a claimed start takes.
const startClaimOwner = "restore-a"

// testBackupStartClaimed starts a backup with a claim: the row is running,
// and the operation is running under a live claim of the starter, so no
// other claimer can take it while the claim lasts.
func testBackupStartClaimed(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	ctx := t.Context()
	r := newBackupRecord(backup.ID(ident.NewID("bkp-")), now())
	before := time.Now()

	op, err := s.StartBackupClaimed(ctx, r, backupStartRequest(r.ID),
		durable.StartClaim{Owner: startClaimOwner, Lease: backupLongLease})
	if err != nil {
		t.Fatalf("start backup with a claim: %v", err)
	}

	if op.Key != backup.OperationKey(r.ID) || op.State != durable.StateRunning || op.Attempt != 1 ||
		op.Claim.Owner != startClaimOwner || op.Claim.Operation != op.ID || op.Claim.Token <= 0 {
		t.Fatalf("started operation = %+v, want a running first attempt under the claim of %s", op, startClaimOwner)
	}

	// The store clock sets the expiry. Half the lease is far from both the
	// start and the full lease, so a skew of the test clock cannot move the
	// check.
	if op.Claim.ExpiresAt.Before(before.Add(backupLongLease / 2)) {
		t.Fatalf("claim expires at %v, want about the lease of %v after %v", op.Claim.ExpiresAt, backupLongLease,
			before)
	}

	if got := mustGetBackup(t, s, r.ID); got.State != backup.StateRunning {
		t.Fatalf("backup row after a claimed start = %+v, want running", got)
	}

	ops := s.DurableOperationsPort()

	_, err = ops.Claim(ctx, durable.ClaimRequest{
		Owner: "worker", Kinds: []durable.Kind{backup.OperationKind}, MaxVersions: nil, Lease: backupLongLease,
		TakeCanceled: false,
	})
	if !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("claim by a worker after a claimed start = %v, want ErrNoWork", err)
	}

	if _, err := ops.Extend(ctx, op.Claim, backupLongLease); err != nil {
		t.Fatalf("renewal of the claim of the start = %v, want nil", err)
	}
}

// testBackupStartClaimedRefused refuses a claimed start while a backup runs,
// with a taken ID and with a claim that is not valid, and stores no row and
// no operation for it.
func testBackupStartClaimedRefused(t *testing.T, newStore BackupFactory) {
	t.Helper()

	s := newStore(t)
	ctx := t.Context()
	claim := durable.StartClaim{Owner: startClaimOwner, Lease: backupLongLease}

	for name, invalid := range map[string]durable.StartClaim{
		"no owner": {Owner: "", Lease: backupLongLease},
		"no lease": {Owner: startClaimOwner, Lease: 0},
	} {
		r := newBackupRecord(backup.ID(ident.NewID("bkp-")), now())

		if _, err := s.StartBackupClaimed(ctx, r, backupStartRequest(r.ID), invalid); !errors.Is(err,
			durable.ErrInvalid) {
			t.Fatalf("claimed start with %s: err = %v, want durable.ErrInvalid", name, err)
		}

		if _, err := s.GetBackup(ctx, r.ID); !errors.Is(err, backup.ErrNotFound) {
			t.Fatalf("row of a claimed start with %s: err = %v, want ErrNotFound", name, err)
		}

		requireNoBackupOperation(t, s, r.ID)
	}

	first := mustStartBackup(t, s, newBackupRecord(backup.ID(ident.NewID("bkp-")), now()))
	second := newBackupRecord(backup.ID(ident.NewID("bkp-")), now().Add(time.Minute))

	if _, err := s.StartBackupClaimed(ctx, second, backupStartRequest(second.ID), claim); !errors.Is(err,
		backup.ErrAlreadyRunning) {
		t.Fatalf("claimed start while a backup runs: err = %v, want ErrAlreadyRunning", err)
	}

	requireNoBackupOperation(t, s, second.ID)

	if _, err := s.StartBackupClaimed(ctx, first, backupStartRequest(first.ID), claim); !errors.Is(err,
		backup.ErrAlreadyExists) {
		t.Fatalf("claimed start with a taken ID: err = %v, want ErrAlreadyExists", err)
	}

	ops := backupOperations(t, s)
	if len(ops) != 1 || ops[0].Key != backup.OperationKey(first.ID) || ops[0].State != durable.StatePending {
		t.Fatalf("operations after refused claimed starts = %+v, want only the pending operation of %s", ops,
			first.ID)
	}
}
