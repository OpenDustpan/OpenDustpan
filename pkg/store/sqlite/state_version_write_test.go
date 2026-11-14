package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// tamperStateVersionContent replaces the recorded content key of the state
// version id through a second database handle, so the next finalize of the
// version matches no row inside its transaction.
func tamperStateVersionContent(t *testing.T, path, id string) {
	t.Helper()

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database = %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database = %v", closeErr)
		}
	})

	_, execErr := conn.ExecContext(t.Context(),
		`UPDATE state_versions SET content_key = 'tampered' WHERE id = ?`, id)

	if execErr != nil {
		t.Fatalf("tamper the recorded content = %v", execErr)
	}
}

// assertFinalizeStillPending fails t when sv is no longer a pending state
// version: a refused finalize must not change the row.
func assertFinalizeStillPending(t *testing.T, s *sqlite.Store, sv workspace.StateVersion) {
	t.Helper()

	stored, err := s.GetStateVersion(t.Context(), sv.ID)
	if err != nil {
		t.Fatalf("GetStateVersion = %v", err)
	}

	if stored.Status != workspace.StatusPending {
		t.Fatalf("status of the refused version = %q, want %q", stored.Status, workspace.StatusPending)
	}
}
