package postgres

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestEmptiedTablesHaveColumnLists proves that each table of
// backup.EmptiedTables has a column list for the coverage check. A table with
// no list would skip its column check.
func TestEmptiedTablesHaveColumnLists(t *testing.T) {
	t.Parallel()

	for _, table := range backup.EmptiedTables {
		t.Run(table, func(t *testing.T) {
			t.Parallel()

			if _, err := pgEmptiedSpec(table); err != nil {
				t.Fatalf("pgEmptiedSpec(%q): %v", table, err)
			}
		})
	}
}

func TestEmptiedSpecRefusesAnUnknownTable(t *testing.T) {
	t.Parallel()

	if _, err := pgEmptiedSpec("no_such_table"); !errors.Is(err, backup.ErrCoverage) {
		t.Fatalf("pgEmptiedSpec(unknown) error = %v, want ErrCoverage", err)
	}
}
