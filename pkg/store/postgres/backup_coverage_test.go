package postgres_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// TestBackupCoverageFailsOnAddedColumn adds the column that the spike of ADR 42
// added, outside the migrations, and proves the coverage check fails with
// ErrCoverage and names the table and the column (ADR 42, decision 5).
func TestBackupCoverageFailsOnAddedColumn(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL backup coverage test", envURL)
	}

	schemaURL := isolatedURL(t, base)

	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	if _, execErr := conn.Exec(t.Context(), `ALTER TABLE stacks ADD COLUMN spike_new_col text`); execErr != nil {
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
