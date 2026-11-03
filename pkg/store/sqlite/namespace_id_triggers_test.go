package sqlite_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// TestNamespaceIDTriggers proves that the database itself refuses a namespace
// ID without the ns- prefix and one more character, on insert and on update.
func TestNamespaceIDTriggers(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "triggers.db")

	s, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	})

	const insert = `INSERT INTO namespaces (name, primary_installation, failover_version, created_at_ms, id)
		VALUES (?, 'eu-west', 1, 0, ?)`

	if _, execErr := conn.ExecContext(ctx, insert, "good", "ns-abc"); execErr != nil {
		t.Fatalf("insert namespace with a valid ID: %v", execErr)
	}

	for i, id := range []string{"", "acme", "ns-", "NS-abc"} {
		if _, execErr := conn.ExecContext(ctx, insert, fmt.Sprintf("bad%d", i), id); execErr == nil {
			t.Fatalf("insert namespace with ID %q: err = nil, want a refusal", id)
		}

		const update = `UPDATE namespaces SET id = ? WHERE name = 'good'`
		if _, execErr := conn.ExecContext(ctx, update, id); execErr == nil {
			t.Fatalf("update namespace to ID %q: err = nil, want a refusal", id)
		}
	}
}
