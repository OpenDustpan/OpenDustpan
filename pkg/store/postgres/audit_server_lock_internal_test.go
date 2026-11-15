package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// TestAppendAuditEntryLocksServerScope proves that an append of the scope
// @server takes the advisory lock of the scope itself, also when the caller
// took no lock. A second connection must not take the lock while the append
// holds it, and must take it after the append commits.
func TestAppendAuditEntryLocksServerScope(t *testing.T) {
	t.Parallel()

	s, _ := createLockSetup(t, "acme")

	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	t.Cleanup(func() {
		rollbackErr := tx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Errorf("roll back the append: %v", rollbackErr)
		}
	})

	if _, err := appendAuditEntry(t.Context(), s.queries.WithTx(tx), serverScopeEntry("server:append")); err != nil {
		t.Fatalf("append a server entry: %v", err)
	}

	if tookServerScopeLock(t, s) {
		t.Fatal("a second connection took the server audit scope lock while the append holds it")
	}

	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit the append: %v", err)
	}

	if !tookServerScopeLock(t, s) {
		t.Fatal("a second connection did not get the server audit scope lock after the append committed")
	}
}

// tookServerScopeLock reports whether a new connection takes the advisory
// lock of the server audit scope. It tries the lock in a transaction of its
// own and ends that transaction.
func tookServerScopeLock(t *testing.T, s *Store) bool {
	t.Helper()

	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	var took bool

	if err := tx.QueryRow(t.Context(), "SELECT pg_try_advisory_xact_lock(418581342580)").Scan(&took); err != nil {
		rollbackErr := tx.Rollback(t.Context())
		if rollbackErr != nil {
			t.Errorf("roll back the try: %v", rollbackErr)
		}

		t.Fatalf("try the server audit scope lock: %v", err)
	}

	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatalf("roll back the try: %v", err)
	}

	return took
}

// TestAppendAuditEntryLockErrorStoresNothing proves that an append whose
// lock fails rolls the transaction back: the store keeps neither the entry
// nor the change of the caller.
func TestAppendAuditEntryLockErrorStoresNothing(t *testing.T) {
	t.Parallel()

	s, _ := createLockSetup(t, "acme")

	_, entriesBefore := serverScopeRows(t, s)

	const principalID = "prn-lock-error"

	at := time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)

	p := principal.Principal{
		CreatedAt:   at,
		UpdatedAt:   at,
		DisabledAt:  time.Time{},
		ID:          principalID,
		Subject:     "local:" + principalID,
		DisplayName: "Lock error",
		Kind:        principal.KindPerson,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
		BreakGlass:  false,
	}

	ran := false

	err := s.inTx(t.Context(), func(q *db.Queries) error {
		if err := insertPrincipal(t.Context(), q, p); err != nil {
			return err
		}

		ran = true

		lockCtx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := appendAuditEntry(lockCtx, q, serverScopeEntry("server:append"))

		return err
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("append with a canceled context: err = %v, want context.Canceled", err)
	}

	if !ran {
		t.Fatal("the caller change did not run before the lock")
	}

	_, entriesAfter := serverScopeRows(t, s)
	if len(entriesAfter) != len(entriesBefore) {
		t.Fatalf("the server audit scope holds %d entries after the refused append, want %d",
			len(entriesAfter), len(entriesBefore))
	}

	if _, err := s.GetPrincipal(t.Context(), principalID); !errors.Is(err, principal.ErrNotFound) {
		t.Fatalf("the caller change after the rollback: err = %v, want principal.ErrNotFound", err)
	}
}
