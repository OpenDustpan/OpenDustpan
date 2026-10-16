package postgres_test

import (
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

				return s.Durable()
			},
			Domain: domain,
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

	op, _, err := s.Durable().Start(t.Context(), durable.StartRequest{Key: "k", Kind: "kind-a", Input: nil})
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

	req := durable.ClaimRequest{Owner: "instance-a", Kinds: []durable.Kind{"kind-a"}, Lease: time.Minute}
	if _, err := s.Durable().Claim(ctx, req); !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("claim of a locked operation: err = %v, want ErrNoWork without a wait for the lock", err)
	}
}

// TestDurableRowsRoundTripThroughAPortableBackup proves that the rows of
// durable operations and their steps go through a portable export and stage
// back with the same counts: a claimed operation with a step, and a pending
// one with no input, whose claim columns are NULL.
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

	seedDurableRows(t, s.Durable())

	ctx := t.Context()
	files, sum, descs := storetest.PortableFiles(t, s)

	want := map[string]int64{backup.TableDurableOperations: 2, backup.TableDurableSteps: 1}
	for table, n := range want {
		if sum.RowCounts[table] != n {
			t.Fatalf("exported rows of %s = %d, want %d", table, sum.RowCounts[table], n)
		}
	}

	staged, err := s.StagePortable(ctx, storetest.PortableOpener(files), sum, descs)
	if err != nil {
		t.Fatalf("stage the portable backup: %v", err)
	}

	t.Cleanup(func() {
		if discardErr := s.DiscardStaging(context.WithoutCancel(ctx)); discardErr != nil {
			t.Errorf("discard staging: %v", discardErr)
		}
	})

	for table, n := range want {
		if staged.RowCounts[table] != n {
			t.Fatalf("staged rows of %s = %d, want %d", table, staged.RowCounts[table], n)
		}
	}
}

// seedDurableRows starts two operations on d, claims the first one, and
// records one step of it.
func seedDurableRows(t *testing.T, d *postgres.DurableStore) {
	t.Helper()

	ctx := t.Context()

	for _, req := range []durable.StartRequest{
		{Key: "claimed", Kind: "kind-a", Input: []byte("input")},
		{Key: "pending", Kind: "kind-b", Input: nil},
	} {
		if _, _, err := d.Start(ctx, req); err != nil {
			t.Fatalf("start %s: %v", req.Key, err)
		}
	}

	req := durable.ClaimRequest{Owner: "instance-a", Kinds: []durable.Kind{"kind-a"}, Lease: time.Hour}

	held, err := d.Claim(ctx, req)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	if err := d.RecordStep(ctx, held.Claim.Step("one"), []byte("result")); err != nil {
		t.Fatalf("record step: %v", err)
	}
}
