package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// fenceOnlyTx is the fenced transaction as it was before the installation
// state check: it reads the failover version FOR SHARE and nothing else. It
// stays in this file only, as the baseline of BenchmarkFencedWrite.
func (s *Store) fenceOnlyTx(ctx context.Context, fence namespace.Fence, fn func(q *db.Queries) error) error {
	return s.inControlTx(ctx, func(q *db.Queries) error {
		version, err := q.GetFailoverVersionForShare(ctx, fence.Namespace.String())
		if errors.Is(err, pgx.ErrNoRows) {
			return namespace.ErrNotFound
		}

		if err != nil {
			return err
		}

		if version != fence.FailoverVersion {
			return namespace.ErrFenced
		}

		return fn(q)
	})
}

// benchStore opens a store in a fresh schema of the test database, and skips
// the benchmark when DUSTPAN_TEST_POSTGRES_URL is not set.
func benchStore(b *testing.B) *Store {
	b.Helper()

	base := os.Getenv("DUSTPAN_TEST_POSTGRES_URL")
	if base == "" {
		b.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL benchmark")
	}

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		b.Fatalf("random schema name: %v", err)
	}

	name := "bench_" + hex.EncodeToString(suffix)

	conn, err := pgx.Connect(b.Context(), base)
	if err != nil {
		b.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(b.Context()); closeErr != nil {
			b.Errorf("close: %v", closeErr)
		}
	}()

	if _, execErr := conn.Exec(b.Context(), "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); execErr != nil {
		b.Fatalf("create schema: %v", execErr)
	}

	u, err := url.Parse(base)
	if err != nil {
		b.Fatalf("parse URL: %v", err)
	}

	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()

	s, err := Open(b.Context(), u.String())
	if err != nil {
		b.Fatalf("open store: %v", err)
	}

	b.Cleanup(s.Close)

	return s
}

// runSerial runs write one call at a time through run.
func runSerial(b *testing.B, run fencedRunner, ns namespace.Namespace, write func(q *db.Queries) error) {
	b.Helper()

	for b.Loop() {
		if err := run(b.Context(), ns.Fence(), write); err != nil {
			b.Fatalf("fenced write: %v", err)
		}
	}
}

// runParallel runs write through run from 8 times GOMAXPROCS goroutines.
func runParallel(b *testing.B, run fencedRunner, ns namespace.Namespace, write func(q *db.Queries) error) {
	b.Helper()

	b.SetParallelism(8)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := run(b.Context(), ns.Fence(), write); err != nil {
				b.Errorf("fenced write: %v", err)

				return
			}
		}
	})
}

// fencedRunner runs one fenced write transaction.
type fencedRunner func(context.Context, namespace.Fence, func(q *db.Queries) error) error

// BenchmarkFencedWrite measures one fenced write transaction with the
// installation state check ("check") and without it ("fence only"), one
// caller at a time and 8 callers for each processor. The write is the same
// INSERT in both. Each write inserts its own row: a write that updated the
// row of the namespace would deadlock with the FOR SHARE lock of the fence.
func BenchmarkFencedWrite(b *testing.B) {
	s := benchStore(b)

	ns := newLockNamespace()
	if err := s.CreateNamespace(b.Context(), ns); err != nil {
		b.Fatalf("create namespace: %v", err)
	}

	var counter atomic.Int64

	write := func(q *db.Queries) error {
		_, err := q.AddSignInFailure(b.Context(), db.AddSignInFailureParams{
			Source: "bench-" + strconv.FormatInt(counter.Add(1), 10), LengthMs: 60_000,
		})

		return err
	}

	runners := map[string]fencedRunner{"fence only": s.fenceOnlyTx, "check": s.inFencedTx}

	for _, name := range []string{"fence only", "check"} {
		b.Run(name+"/serial", func(b *testing.B) { runSerial(b, runners[name], ns, write) })
		b.Run(name+"/parallel8", func(b *testing.B) { runParallel(b, runners[name], ns, write) })
	}
}
