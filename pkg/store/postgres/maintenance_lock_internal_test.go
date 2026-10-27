package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// writeLockWait is how long a case waits to see that a call stays blocked.
const writeLockWait = 300 * time.Millisecond

// assertBlocked fails the test when done answers before writeLockWait passes.
func assertBlocked(t *testing.T, done <-chan error, what string) {
	t.Helper()

	select {
	case err := <-done:
		t.Fatalf("%s returned while the lock was held: %v", what, err)
	case <-time.After(writeLockWait):
	}
}

// commit ends tx and fails the test on an error.
func commit(t *testing.T, tx pgx.Tx) {
	t.Helper()

	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestEnterWaitsForWriteInFlight proves that an enter waits while a write
// transaction holds the shared write lock, and goes on when that transaction
// ends.
func TestEnterWaitsForWriteInFlight(t *testing.T) {
	t.Parallel()

	s := openKeepLockStore(t, testPostgresURL(t))
	ctx := t.Context()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if _, err = tx.Exec(ctx, writeLockSQL, writeLockClass); err != nil {
		t.Fatalf("take the shared lock: %v", err)
	}

	done := make(chan error, 1)

	go func() { done <- s.SetStateIf(ctx, maintenance.StateNormal, maintenance.StateMaintenance) }()

	assertBlocked(t, done, "enter")
	commit(t, tx)

	if err = <-done; err != nil {
		t.Fatalf("enter after the write ended: %v", err)
	}
}

// TestWriteWaitsForEnterAndReadsItsState proves that a write waits while an
// enter holds the exclusive write lock, and then reads the state that the
// enter stored: the write fails with maintenance.ErrInMaintenance.
func TestWriteWaitsForEnterAndReadsItsState(t *testing.T) {
	t.Parallel()

	s := openKeepLockStore(t, testPostgresURL(t))
	ctx := t.Context()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if err = lockWritesExclusive(ctx, s.queries.WithTx(tx), 0, maintenance.ErrUnavailable); err != nil {
		t.Fatalf("take the exclusive lock: %v", err)
	}

	if _, err = tx.Exec(ctx, `UPDATE installation SET state = 'maintenance' WHERE id = 1`); err != nil {
		t.Fatalf("store maintenance: %v", err)
	}

	done := make(chan error, 1)

	go func() { done <- s.CreateNamespace(context.WithoutCancel(ctx), newLockNamespace()) }()

	assertBlocked(t, done, "write")
	commit(t, tx)

	if err = <-done; !errors.Is(err, maintenance.ErrInMaintenance) {
		t.Fatalf("write after the enter committed: err = %v, want ErrInMaintenance", err)
	}
}

func newLockNamespace() namespace.Namespace {
	return namespace.Namespace{
		CreatedAt: time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC), ID: namespace.NewID(),
		Name: "locked", PrimaryInstallation: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
}

// TestEnterGivesUpWhenAWriteDoesNotEnd proves that an enter does not wait
// without limit for a write transaction that holds the shared write lock: it
// reports maintenance.ErrBusy after the lock timeout, changes nothing,
// and a leave and a later enter work after the write ends.
func TestEnterGivesUpWhenAWriteDoesNotEnd(t *testing.T) {
	t.Parallel()

	s := openKeepLockStore(t, testPostgresURL(t))
	s.stateLockWait = 200 * time.Millisecond
	ctx := t.Context()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if _, err = tx.Exec(ctx, writeLockSQL, writeLockClass); err != nil {
		t.Fatalf("take the shared lock: %v", err)
	}

	for name, run := range map[string]func() error{
		"SetStateIf": func() error {
			return s.SetStateIf(ctx, maintenance.StateNormal, maintenance.StateMaintenance)
		},
		"SetState": func() error { return s.SetState(ctx, maintenance.StateMaintenance) },
	} {
		if err = run(); !errors.Is(err, maintenance.ErrBusy) || errors.Is(err, maintenance.ErrUnavailable) {
			t.Fatalf("%s while a write does not end: err = %v, want ErrBusy", name, err)
		}
	}

	commit(t, tx)

	if got, getErr := s.GetState(ctx); getErr != nil || got != maintenance.StateNormal {
		t.Fatalf("state after the refused enters = %q, %v, want normal without error", got, getErr)
	}

	if err = s.SetStateIf(ctx, maintenance.StateNormal, maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter after the write ended: %v", err)
	}
}
