package sqlite

import (
	"bytes"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// stageExport exports s and stages the export for a restore.
func stageExport(t *testing.T, s *Store) {
	t.Helper()

	sink := &keepMemSink{files: map[string]*bytes.Buffer{}}

	sum, files, err := s.ExportPortable(t.Context(), sink.open)
	if err != nil {
		t.Fatalf("export portable: %v", err)
	}

	summary, err := s.StagePortable(t.Context(), sink.read, sum, files, storetest.StageAs(t, s))
	if err != nil || summary.RowCounts == nil {
		t.Fatalf("stage portable: %v, row counts %v", err, summary.RowCounts)
	}
}

// swapCases are the two swaps of a restore.
func swapCases() []struct {
	name  string
	apply func(t *testing.T, s *Store) error
} {
	return []struct {
		name  string
		apply func(t *testing.T, s *Store) error
	}{
		{name: "swap", apply: func(t *testing.T, s *Store) error {
			t.Helper()

			return s.ApplyStaging(t.Context())
		}},
		{name: "swap keeping access", apply: func(t *testing.T, s *Store) error {
			t.Helper()

			_, err := s.ApplyStagingKeepingAccess(t.Context())

			return err
		}},
	}
}

// TestRestoreOnBinaryBelowMinimum proves that a restore on a binary below the
// minimum of the live database is refused at its start and at the swap, and
// that the live database and its minimum stay as they were. The staged file
// that such a binary built holds its own schema_migrations: swapped in, it
// would lower the minimum and remove the tables that newer binaries use.
func TestRestoreOnBinaryBelowMinimum(t *testing.T) {
	t.Parallel()

	for _, tt := range swapCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := openWithBinary(t, "v0.4.0")
			ns := createAcme(t, s)

			stageExport(t, s)

			if err := s.apply(t.Context(), contractMigration("v0.5.0")); err != nil {
				t.Fatalf("apply the contract migration: %v", err)
			}

			if err := s.CheckBinaryMinimum(t.Context()); !errors.Is(err, schema.ErrBinaryBelowMinimum) {
				t.Fatalf("start check = %v, want ErrBinaryBelowMinimum", err)
			}

			if err := tt.apply(t, s); !errors.Is(err, schema.ErrBinaryBelowMinimum) {
				t.Fatalf("swap = %v, want ErrBinaryBelowMinimum", err)
			}

			requireApplied(t, s, 9999)

			if _, err := s.GetNamespace(t.Context(), ns.Name); err != nil {
				t.Fatalf("live namespace after the refused swap: %v", err)
			}
		})
	}
}

// requireApplied fails when the newest applied migration of s is not the one
// with version and the minimum v0.5.0.
func requireApplied(t *testing.T, s *Store, version int64) {
	t.Helper()

	applied, err := s.AppliedMigrations(t.Context())
	if err != nil || len(applied) == 0 {
		t.Fatalf("applied migrations = %v, %v", applied, err)
	}

	if last := applied[len(applied)-1]; last.Version != version || last.MinBinaryVersion != "v0.5.0" {
		t.Fatalf("newest applied migration = %+v, want %d with the minimum v0.5.0", last, version)
	}
}
