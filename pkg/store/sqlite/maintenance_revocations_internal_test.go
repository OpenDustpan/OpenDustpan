package sqlite

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestRestoreFailsClosedOnAJournalRowOfAnUnknownKind proves what the restore
// does with a journal row of a kind that no store writes. Only a write that
// turns the CHECK constraints off can put such a row in the journal. The
// restore copies the live journal into the staged file, whose table holds the
// same CHECK, so the copy fails and the whole restore fails. Nothing changes:
// the live file keeps its rows and the staged file stays unapplied. The
// replay checks the kind too, but the constraint stops the row first.
func TestRestoreFailsClosedOnAJournalRowOfAnUnknownKind(t *testing.T) {
	t.Parallel()

	s := openSeededKeepStore(t)

	if err := s.SetStateIf(t.Context(), maintenance.StateNormal, maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	conn, err := s.conn.Conn(t.Context())
	if err != nil {
		t.Fatalf("open a connection: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close the connection: %v", closeErr)
		}
	}()

	if _, err = conn.ExecContext(t.Context(), `PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatalf("turn off the check constraints: %v", err)
	}

	const insert = `INSERT INTO maintenance_revocations (kind, target, end_reason, actor, object, at_ms)
VALUES ('wipe', 'tok-1', '', 'mallory', 'token:x', 1)`

	if _, err = conn.ExecContext(t.Context(), insert); err != nil {
		t.Fatalf("insert the hostile row: %v", err)
	}

	if _, err = conn.ExecContext(t.Context(), `PRAGMA ignore_check_constraints = OFF`); err != nil {
		t.Fatalf("turn on the check constraints: %v", err)
	}

	if applyErr := s.ApplyStaging(t.Context()); applyErr == nil {
		t.Fatal("apply with a hostile journal row succeeded, want a failed restore")
	}

	if _, nsErr := s.GetNamespace(t.Context(), namespace.Name("acme")); errors.Is(nsErr, namespace.ErrNotFound) {
		t.Fatalf("namespace after the failed restore: %v, want the live data unchanged", nsErr)
	}
}
