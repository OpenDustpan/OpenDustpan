package postgres_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// createReleaseWorkspace creates the workspace acme/web in s and returns it.
func createReleaseWorkspace(t *testing.T, s *postgres.Store) workspace.Workspace {
	t.Helper()

	fence := ensureNamespace(t, s, "acme")
	at := time.Date(2026, time.November, 1, 9, 0, 0, 0, time.UTC)
	w := workspace.Workspace{
		CreatedAt:   at,
		UpdatedAt:   at,
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          workspace.ID(ident.NewID("ws-")),
		Ref:         workspace.Ref{Namespace: "acme", Name: "web"},
		NamespaceID: "",
		Revision:    1,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}

	if err := s.CreateWorkspace(t.Context(), w, fence); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	return w
}

// storeReleaseKind writes kind as the kind of the last release of the
// workspace id with direct SQL, with the CHECK of the column in place, and
// returns the error of the write.
func storeReleaseKind(t *testing.T, schemaURL string, id workspace.ID, kind string) error {
	t.Helper()

	conn := rawSettingConn(t, schemaURL)
	defer closeSettingConn(t, conn)

	_, err := conn.Exec(t.Context(), `UPDATE workspaces SET last_release_kind = $1 WHERE id = $2`, kind, id.String())
	if err != nil {
		return fmt.Errorf("update the kind of the last release: %w", err)
	}

	return nil
}

// TestLastReleaseKindColumnRefusesAnotherValue proves the layer of the table
// alone: the CHECK of the column refuses a kind that is not empty, normal or
// forced, also when the store code is skipped.
func TestLastReleaseKindColumnRefusesAnotherValue(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL test", envURL)
	}

	schemaURL := isolatedURL(t, base)
	s := openURL(t, schemaURL)
	w := createReleaseWorkspace(t, s)

	for _, bad := range []string{"expired", "Normal", "forced ", "normal\n"} {
		err := storeReleaseKind(t, schemaURL, w.ID, bad)
		if err == nil || !strings.Contains(err.Error(), "workspaces_last_release_kind_check") {
			t.Fatalf("store the kind %q = %v, want the refusal of the CHECK of the column", bad, err)
		}
	}

	for _, good := range []string{"normal", "forced", ""} {
		if err := storeReleaseKind(t, schemaURL, w.ID, good); err != nil {
			t.Fatalf("store the kind %q: %v", good, err)
		}
	}
}

// TestLastReleaseKindReadRefusesAnotherValue proves the layer of the read
// alone: a row that holds another kind, written with the CHECK dropped as a
// row of another version could hold it, reads as no record.
func TestLastReleaseKindReadRefusesAnotherValue(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL test", envURL)
	}

	schemaURL := isolatedURL(t, base)
	s := openURL(t, schemaURL)
	w := createReleaseWorkspace(t, s)

	conn := rawSettingConn(t, schemaURL)
	defer closeSettingConn(t, conn)

	const drop = `ALTER TABLE workspaces DROP CONSTRAINT IF EXISTS workspaces_last_release_kind_check`
	if _, err := conn.Exec(t.Context(), drop); err != nil {
		t.Fatalf("drop the check of the kind: %v", err)
	}

	_, err := conn.Exec(t.Context(),
		`UPDATE workspaces SET last_release_kind = 'expired', last_release_lock_id = 'lock-a',
		 last_release_holder = 'bob', last_release_by = 'bob', last_release_at_ms = 1 WHERE id = $1`, w.ID.String())
	if err != nil {
		t.Fatalf("plant the kind: %v", err)
	}

	got, err := s.GetWorkspaceByID(t.Context(), w.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	if got.LastRelease != workspace.NoRelease() {
		t.Fatalf("record = %+v, want no record", got.LastRelease)
	}
}
