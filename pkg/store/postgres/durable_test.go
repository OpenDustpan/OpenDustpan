package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestDurableConformance runs the durable operations suite. Each case gets a
// fresh schema, and each handle of a case is a store of its own on that
// schema, as two instances on one database are.
func TestDurableConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL durable operations suite", envURL)
	}

	durabletest.Run(t, func(t *testing.T) durabletest.Harness {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		first, err := postgres.Open(t.Context(), schemaURL)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(first.Close)

		domain, err := postgres.DurableDomainForTest(t.Context(), first)
		if err != nil {
			t.Fatalf("domain: %v", err)
		}

		return durabletest.Harness{
			// The adapter reads every time from the database clock and none
			// in Go, so the skew of an instance has nothing to move.
			Open: func(t *testing.T, _ time.Duration) durable.Store {
				t.Helper()

				s, openErr := postgres.Open(t.Context(), schemaURL)
				if openErr != nil {
					t.Fatalf("open store: %v", openErr)
				}

				t.Cleanup(s.Close)

				return s.DurableOperations()
			},
			Domain: domain,
		}
	})
}

// TestDurableScheduleConformance runs the schedule suite. Each case gets a
// fresh schema, and each handle of a case is a store of its own on that
// schema, as two instances on one database are.
func TestDurableScheduleConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL durable schedule suite", envURL)
	}

	durabletest.RunSchedules(t, func(t *testing.T) durabletest.ScheduleHarness {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		first, err := postgres.Open(t.Context(), schemaURL)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(first.Close)

		return durabletest.ScheduleHarness{
			Open: func(t *testing.T, _ time.Duration) durabletest.ScheduledStore {
				t.Helper()

				s, openErr := postgres.Open(t.Context(), schemaURL)
				if openErr != nil {
					t.Fatalf("open store: %v", openErr)
				}

				t.Cleanup(s.Close)

				return s.DurableOperations()
			},
		}
	})
}

// TestDurableHeartbeatConformance runs the heartbeat suite. Each case gets a
// fresh schema, and each handle of a case is a store of its own on that
// schema, as two instances on one database are.
func TestDurableHeartbeatConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL durable heartbeat suite", envURL)
	}

	durabletest.RunHeartbeats(t, func(t *testing.T) durabletest.HeartbeatHarness {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		first, err := postgres.Open(t.Context(), schemaURL)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(first.Close)

		return durabletest.HeartbeatHarness{
			Open: func(t *testing.T, _ time.Duration) durable.HeartbeatStore {
				t.Helper()

				s, openErr := postgres.Open(t.Context(), schemaURL)
				if openErr != nil {
					t.Fatalf("open store: %v", openErr)
				}

				t.Cleanup(s.Close)

				return s.DurableOperations()
			},
			Seed: func(t *testing.T, owner string, at time.Time, lease time.Duration) {
				t.Helper()

				if err := postgres.SeedHeartbeatRow(t.Context(), first, owner, at, lease); err != nil {
					t.Fatalf("seed heartbeat: %v", err)
				}
			},
		}
	})
}

// TestDurableClaimSkipsALockedRow proves that a claim skips the row of an
// operation that another transaction holds, and answers ErrNoWork at once,
// instead of a wait for that transaction.
func TestDurableClaimSkipsALockedRow(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL durable claim lock test", envURL)
	}

	schemaURL := isolatedURL(t, base)

	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	op, _, err := s.DurableOperations().
		Start(t.Context(), durable.StartRequest{Key: "k", Kind: "kind-a", Version: 0, Input: nil})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	defer func() {
		if rollbackErr := tx.Rollback(t.Context()); rollbackErr != nil {
			t.Errorf("rollback: %v", rollbackErr)
		}
	}()

	if _, err := tx.Exec(t.Context(), `SELECT id FROM durable_operations WHERE id = $1 FOR UPDATE`, op.ID); err != nil {
		t.Fatalf("lock the operation row: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	req := durable.ClaimRequest{
		Owner:        "instance-a",
		Kinds:        []durable.Kind{"kind-a"},
		MaxVersions:  nil,
		Lease:        time.Minute,
		TakeCanceled: false,
	}
	if _, err := s.DurableOperations().Claim(ctx, req); !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("claim of a locked operation: err = %v, want ErrNoWork without a wait for the lock", err)
	}
}

// TestDurableRowsRoundTripThroughAPortableBackup proves that a portable backup
// holds no rows of the durable control tables and that a restore keeps the
// live rows unchanged: a claimed operation with a step, and a pending one
// with no input, whose claim columns are NULL.
func TestDurableRowsRoundTripThroughAPortableBackup(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL durable backup test", envURL)
	}

	s, err := postgres.Open(t.Context(), isolatedURL(t, base))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	ops := s.DurableOperations()
	if ops == nil {
		t.Fatal("durable operations are nil, want *postgres.DurableOperations")
	}

	seedDurableRows(t, ops)

	ctx := t.Context()

	before, listErr := s.DurableOperations().List(ctx, durable.Filter{Kind: "", States: nil, Limit: 0})
	if listErr != nil {
		t.Fatalf("list durable operations before the backup: %v", listErr)
	}

	if len(before) != 2 {
		t.Fatalf("durable operations before the backup = %d, want 2", len(before))
	}

	files, sum, descs := storetest.PortableFiles(t, s)
	checkBackupHoldsNoDurable(t, sum, descs, files)

	staged, stageErr := s.StagePortable(ctx, storetest.PortableOpener(files), sum, descs, storetest.StageAs(t, s))
	if stageErr != nil {
		t.Fatalf("stage the portable backup: %v", stageErr)
	}

	t.Cleanup(func() {
		if discardErr := s.DiscardStaging(context.WithoutCancel(ctx)); discardErr != nil {
			t.Errorf("discard staging: %v", discardErr)
		}
	})

	checkStagedHoldsNoDurable(t, staged)

	if applyErr := s.ApplyStaging(ctx); applyErr != nil {
		t.Fatalf("apply the staged backup: %v", applyErr)
	}

	after, afterErr := s.DurableOperations().List(ctx, durable.Filter{Kind: "", States: nil, Limit: 0})
	if afterErr != nil {
		t.Fatalf("list durable operations after the restore: %v", afterErr)
	}

	checkDurableUnchanged(t, before, after)
}

// checkBackupHoldsNoDurable proves that the portable export holds no file of
// either durable control table.
func checkBackupHoldsNoDurable(
	t *testing.T, sum backup.Summary, descs []backup.PortableFile, files map[string][]byte,
) {
	t.Helper()

	for _, table := range []string{backup.TableDurableOperations, backup.TableDurableSteps} {
		if n, ok := sum.RowCounts[table]; ok {
			t.Fatalf("exported rows of control table %s = %d, want no rows in the backup", table, n)
		}

		for _, d := range descs {
			if d.Table == table {
				t.Fatalf("backup holds a file for control table %s, want none", table)
			}
		}

		if _, ok := files[backup.PortablePath(table)]; ok {
			t.Fatalf("backup holds bytes for control table %s, want none", table)
		}
	}
}

// checkStagedHoldsNoDurable proves that the staged summary holds no rows of
// either durable control table.
func checkStagedHoldsNoDurable(t *testing.T, staged backup.Summary) {
	t.Helper()

	for _, table := range []string{backup.TableDurableOperations, backup.TableDurableSteps} {
		if n, ok := staged.RowCounts[table]; ok {
			t.Fatalf("staged rows of control table %s = %d, want no rows in the backup", table, n)
		}
	}
}

// checkDurableUnchanged proves that the durable operations after the restore
// match those before it, with the same claims and steps.
func checkDurableUnchanged(t *testing.T, before, after []durable.Operation) {
	t.Helper()

	if len(after) != len(before) {
		t.Fatalf("durable operations after the restore = %d, want %d", len(after), len(before))
	}

	byID := make(map[durable.ID]durable.Operation, len(after))
	for i := range after {
		byID[after[i].ID] = after[i]
	}

	for i := range before {
		want := before[i]
		got, ok := byID[want.ID]
		if !ok {
			t.Fatalf("durable operation %s missing after the restore", want.ID)
		}

		checkOneDurableUnchanged(t, want, got)
	}
}

// checkOneDurableUnchanged proves that one restored operation keeps its
// identity, claim and steps.
func checkOneDurableUnchanged(t *testing.T, want, got durable.Operation) {
	t.Helper()

	if got.Key != want.Key || got.Kind != want.Kind || got.State != want.State ||
		got.Claim.Token != want.Claim.Token || got.Claim.Owner != want.Claim.Owner ||
		len(got.Steps) != len(want.Steps) {
		t.Fatalf("durable operation %s after the restore = %+v, want %+v", want.ID, got, want)
	}

	for i, step := range want.Steps {
		if got.Steps[i].Name != step.Name || !bytes.Equal(got.Steps[i].Result, step.Result) {
			t.Fatalf("durable operation %s step %d after the restore = %+v, want %+v",
				want.ID, i, got.Steps[i], step)
		}
	}
}

// seedDurableRows starts two operations on d, claims the first one, and
// records one step of it.
func seedDurableRows(t *testing.T, d *postgres.DurableOperations) {
	t.Helper()

	ctx := t.Context()

	for _, req := range []durable.StartRequest{
		{Key: "claimed", Kind: "kind-a", Version: 0, Input: []byte("input")},
		{Key: "pending", Kind: "kind-b", Version: 0, Input: nil},
	} {
		if _, _, err := d.Start(ctx, req); err != nil {
			t.Fatalf("start %s: %v", req.Key, err)
		}
	}

	req := durable.ClaimRequest{
		Owner:        "instance-a",
		Kinds:        []durable.Kind{"kind-a"},
		MaxVersions:  nil,
		Lease:        time.Hour,
		TakeCanceled: false,
	}

	held, err := d.Claim(ctx, req)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	if err := d.RecordStep(ctx, held.Claim.Step("one"), []byte("result")); err != nil {
		t.Fatalf("record step: %v", err)
	}
}
