package sqlite_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// TestBackupCoverageFailsOnAddedColumn adds the column that the spike of ADR 42
// added, outside the migrations, and proves the coverage check fails with
// ErrCoverage and names the table and the column (ADR 42, decision 5).
func TestBackupCoverageFailsOnAddedColumn(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close database: %v", closeErr)
		}
	}()

	if _, execErr := conn.ExecContext(t.Context(), `ALTER TABLE stacks ADD COLUMN spike_new_col TEXT`); execErr != nil {
		t.Fatalf("add column: %v", execErr)
	}

	err = s.CheckCoverage(t.Context())
	if err == nil {
		t.Fatal("coverage after an added column: err = nil, want ErrCoverage")
	}

	if !errors.Is(err, backup.ErrCoverage) {
		t.Fatalf("coverage after an added column: err = %v, want ErrCoverage", err)
	}

	if !strings.Contains(err.Error(), `"stacks"`) || !strings.Contains(err.Error(), `"spike_new_col"`) {
		t.Fatalf("coverage error = %v, want the table %q and the column %q", err, "stacks", "spike_new_col")
	}
}
