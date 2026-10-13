package backup

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// ErrCoverage reports that the column list of a backup table and the live
// schema of the store differ (ADR 42, decision 5). A column that the list does
// not hold, or a listed column that the schema does not hold, fails the export.
// The error names the table and the column.
var ErrCoverage = errors.New("backup column list and schema differ")

// Coverage is the port that compares the column list of each table in Tables
// with the live schema of the store. It returns ErrCoverage when they differ.
// The conformance suite runs it on both dialects.
type Coverage interface {
	CheckCoverage(ctx context.Context) error
}

// CheckColumns compares the column list of table with its live columns. The
// order of the names does not matter. It reports the first column that the
// list omits, and then the first listed column that the schema does not hold.
func CheckColumns(table string, listed, live []string) error {
	for _, column := range live {
		if !slices.Contains(listed, column) {
			return fmt.Errorf(
				"backup column list of table %q misses column %q: %w",
				table,
				column,
				ErrCoverage,
			)
		}
	}

	for _, column := range listed {
		if !slices.Contains(live, column) {
			return fmt.Errorf(
				"backup column list of table %q names unknown column %q: %w",
				table,
				column,
				ErrCoverage,
			)
		}
	}

	return nil
}
