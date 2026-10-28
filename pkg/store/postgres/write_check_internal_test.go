package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// legacyReadStateSQL is the read-state statement from before the minimum
// binary version joined it. The benchmark runs it as the baseline.
const legacyReadStateSQL = `SELECT state FROM installation WHERE id = 1`

// openBench opens a migrated schema as a binary of version v0.5.0, and applies
// a contract migration with minimum when it is not empty.
func openBench(b *testing.B, minimum string) *Store {
	b.Helper()

	base := os.Getenv(membershipURLEnv)
	if base == "" {
		b.Skipf("set %s to run the PostgreSQL benchmark", membershipURLEnv)
	}

	s, err := Open(b.Context(), pgtest.MigratedURL(b, base), WithBinaryVersion("v0.5.0"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}

	b.Cleanup(s.Close)

	if minimum != "" {
		err = pgx.BeginFunc(b.Context(), s.pool, func(tx pgx.Tx) error {
			return apply(b.Context(), tx, contractMigration(minimum))
		})
		if err != nil {
			b.Fatalf("apply contract migration: %v", err)
		}
	}

	return s
}

// legacyCheck runs the lock and the state read of the statement from before
// the minimum joined it.
func legacyCheck(ctx context.Context, tx pgx.Tx) error {
	var batch pgx.Batch

	batch.Queue(writeLockSQL, writeLockClass)
	batch.Queue(legacyReadStateSQL)

	results := tx.SendBatch(ctx, &batch)

	var state string

	_, lockErr := results.Exec()

	var scanErr error
	if lockErr == nil {
		scanErr = results.QueryRow().Scan(&state)
	}

	if err := errors.Join(lockErr, scanErr, results.Close()); err != nil {
		return fmt.Errorf("legacy check: %w", err)
	}

	return nil
}

// benchWriteCheck runs check in a transaction of s once for each iteration.
func benchWriteCheck(b *testing.B, s *Store, check func(ctx context.Context, tx pgx.Tx) error) {
	b.Helper()
	b.ReportAllocs()

	for b.Loop() {
		err := pgx.BeginFunc(b.Context(), s.pool, func(tx pgx.Tx) error { return check(b.Context(), tx) })
		if err != nil {
			b.Fatalf("check: %v", err)
		}
	}
}

// BenchmarkWriteCheck records the cost that the minimum binary version adds
// to the check that every write transaction runs: the lock, the state read
// and the minimum read, in one round trip, with an empty transaction body.
// "state only" is the statement from before the minimum joined it. The
// "minimum" cases run the current statement, with no minimum in the schema
// and with one that the binary meets. The last case adds the fence.
func BenchmarkWriteCheck(b *testing.B) {
	b.Run("state only", func(b *testing.B) {
		benchWriteCheck(b, openBench(b, ""), legacyCheck)
	})

	for _, tc := range []struct{ name, minimum string }{
		{name: "state and minimum, none set", minimum: ""},
		{name: "state and minimum, one set", minimum: "v0.5.0"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			s := openBench(b, tc.minimum)

			benchWriteCheck(b, s, func(ctx context.Context, tx pgx.Tx) error {
				_, err := readWritable(ctx, tx, readStateSQL, nil, s.binaryVersion)

				return err
			})
		})
	}

	b.Run("state, fence and minimum, one set", benchFencedCheck)
}

// benchFencedCheck runs the state, fence and minimum check of a fenced write.
func benchFencedCheck(b *testing.B) {
	s := openBench(b, "v0.5.0")

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Now().UTC(), Name: "acme", PrimaryInstallation: "eu-west",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(b.Context(), ns); err != nil {
		b.Fatalf("create namespace: %v", err)
	}

	b.ReportAllocs()

	for b.Loop() {
		if err := s.inFencedTx(b.Context(), ns.Fence(), func(*db.Queries) error { return nil }); err != nil {
			b.Fatalf("fenced check: %v", err)
		}
	}
}
