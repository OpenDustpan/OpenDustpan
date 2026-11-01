package sqlite

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/localuserstore"
)

// TestLocalUserConstraints is the last layer of the local user table: a row
// that skips every check of the code still fails at the database.
func TestLocalUserConstraints(t *testing.T) {
	t.Parallel()

	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	at := time.UnixMilli(1_700_000_000_000).UTC()
	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-1", Subject: "local:lus-1",
		DisplayName: "Alice", Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "",
		GroupCount: 0, BreakGlass: false,
	}

	e := prefsEntry(p.ID, at)
	e.Action = "server:manage-principals"

	if err := s.CreatePrincipal(t.Context(), p, e); err != nil {
		t.Fatalf("create principal: %v", err)
	}

	insert := func(r localuserstore.HostileRow) error {
		_, err := s.conn.ExecContext(t.Context(), `INSERT INTO local_users
			(id, principal_id, user_name, password_hash, password_set_at_ms, change_required, demo, created_by, created_at_ms)
			VALUES ('lus-1', 'prn-1', ?, ?, ?, ?, 0, 'admin', 1)`, r.UserName, r.PasswordHash, r.SetAtMs, r.Change)
		if err != nil {
			return fmt.Errorf("insert local user row: %w", err)
		}

		return nil
	}

	for _, r := range localuserstore.HostileRows() {
		if err := insert(r); err == nil {
			t.Errorf("insert %s: err = nil, want a constraint error", r.Name)
		}
	}

	if err := insert(localuserstore.ValidRow()); err != nil {
		t.Fatalf("insert a valid row: %v", err)
	}
}
