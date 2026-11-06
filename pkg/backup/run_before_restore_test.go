package backup_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
)

// claimingExporter is an exporter that, while the backup writes, asks for the
// operation as an idle worker does, and keeps the answer.
type claimingExporter struct {
	stub    *stubExporter
	ops     durable.Store
	claimed error
}

func (e *claimingExporter) ExportPortable(
	ctx context.Context, sink backup.PortableSink,
) (backup.Summary, []backup.PortableFile, error) {
	_, e.claimed = e.ops.Claim(ctx, durable.ClaimRequest{
		Owner: "worker", Kinds: []durable.Kind{backup.OperationKind}, MaxVersions: nil, Lease: time.Minute,
		TakeCanceled: false,
	})

	return e.stub.ExportPortable(ctx, sink)
}

// TestRunBeforeRestoreRunsTheBackupOnTheCaller proves that the backup before a
// restore runs to its end on the goroutine of the restore, under a claim that
// its start took, so that a worker cannot take it while it runs.
func TestRunBeforeRestoreRunsTheBackupOnTheCaller(t *testing.T) {
	t.Parallel()

	exporter := &claimingExporter{
		stub: &stubExporter{sum: testSummary(), data: []byte("sql"), err: nil}, ops: nil, claimed: nil,
	}
	fix := newFixture(t, exporter, 0)
	exporter.ops = fix.registry.ops

	if err := fix.service.RunBeforeRestore(t.Context(), "bkp-before", backup.MintRestoreID()); err != nil {
		t.Fatalf("RunBeforeRestore = %v, want nil", err)
	}

	if !errors.Is(exporter.claimed, durable.ErrNoWork) {
		t.Fatalf("claim of a worker while the backup wrote = %v, want ErrNoWork", exporter.claimed)
	}

	row, err := fix.service.Get(t.Context(), "bkp-before")
	if err != nil || row.State != backup.StateSucceeded {
		t.Fatalf("backup row after RunBeforeRestore = %+v, %v; want succeeded", row, err)
	}

	ops, err := fix.registry.ops.List(t.Context(), durable.Filter{Kind: backup.OperationKind, States: nil, Limit: 0})
	if err != nil || len(ops) != 1 || ops[0].State != durable.StateSucceeded || ops[0].Attempt != 1 {
		t.Fatalf("backup operations = %+v, %v; want one that succeeded in its first attempt", ops, err)
	}
}

// TestRunBeforeRestoreRefusesATakenID proves that RunBeforeRestore with the ID
// of a backup that exists returns ErrAlreadyExists and runs nothing.
func TestRunBeforeRestoreRefusesATakenID(t *testing.T) {
	t.Parallel()

	fix := newFixture(t, &stubExporter{sum: testSummary(), data: []byte("sql"), err: nil}, 0)
	restore := backup.MintRestoreID()

	if err := fix.service.StartBeforeRestore(t.Context(), "bkp-taken", restore); err != nil {
		t.Fatalf("StartBeforeRestore: %v", err)
	}

	if err := fix.service.RunBeforeRestore(t.Context(), "bkp-taken", restore); !errors.Is(err,
		backup.ErrAlreadyExists) {
		t.Fatalf("RunBeforeRestore with a taken ID = %v, want ErrAlreadyExists", err)
	}

	ops, err := fix.registry.ops.List(t.Context(), durable.Filter{Kind: backup.OperationKind, States: nil, Limit: 0})
	if err != nil || len(ops) != 1 || ops[0].State != durable.StatePending {
		t.Fatalf("backup operations = %+v, %v; want the one pending operation of the first start", ops, err)
	}
}
