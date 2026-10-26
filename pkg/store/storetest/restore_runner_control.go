package storetest

import (
	"testing"
)

// testRestoreKeepsRunnerControlRows proves that a restore keeps the live
// control rows of the runner protocol in the destination: a live claim,
// handoff token, job session and nonce are still there after the swap. The
// rows describe claims of the live durable operations, so a restore that drops
// them leaves a runner with a claim that the server no longer knows.
func testRestoreKeepsRunnerControlRows(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	src := newStore(t)
	dst := newStore(t)

	seeder, ok := dst.(RunnerSeeder)
	if !ok {
		t.Skip("the store keeps no runner control rows")
	}

	ps := portableRestoreStores(t, src, dst)
	ctx := t.Context()

	if err := seeder.SeedRunnerControlRows(ctx); err != nil {
		t.Fatalf("seed the live runner control rows: %v", err)
	}

	seedBackupStore(t, ps.src)
	sink, sum, files := exportPortable(t, ps.src)

	if _, err := ps.dst.StagePortable(ctx, sink.read, sum, files, StageAs(t, ps.dst)); err != nil {
		t.Fatalf("stage portable backup: %v", err)
	}

	if err := ps.dst.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staged backup: %v", err)
	}

	counts, err := seeder.CountRunnerControlRows(ctx)
	if err != nil {
		t.Fatalf("count runner control rows after the restore: %v", err)
	}

	for _, table := range runnerControlTables() {
		if counts[table] != 1 {
			t.Errorf("rows of control table %q after the restore = %d, want the 1 live row", table, counts[table])
		}
	}
}
