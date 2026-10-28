package postgres

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// stageExport exports s and stages the export for a restore.
func stageExport(t *testing.T, s *Store) {
	t.Helper()

	sink := &keepLockSink{files: map[string]*bytes.Buffer{}}

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
// minimum of the live schema is refused at its start and in the swap
// transaction, and that the live schema and its minimum stay as they were. The
// staging schema that such a binary built holds its own schema_migrations:
// swapped in, it would lower the minimum and remove the tables that newer
// binaries use. The old handle stands for the old binary, and a second handle
// stands for the new binary that raises the minimum.
func TestRestoreOnBinaryBelowMinimum(t *testing.T) {
	t.Parallel()

	for _, tt := range swapCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			url := membershipURL(t)
			old := openAt(t, url, "v0.4.0")
			ns := createAcme(t, old)

			stageExport(t, old)

			current := openAt(t, url, "v0.5.0")
			applyContract(t, current, "v0.5.0")

			if err := old.CheckBinaryMinimum(t.Context()); !errors.Is(err, schema.ErrBinaryBelowMinimum) {
				t.Fatalf("start check = %v, want ErrBinaryBelowMinimum", err)
			}

			if err := current.CheckBinaryMinimum(t.Context()); err != nil {
				t.Fatalf("start check of the binary at the minimum = %v, want nil", err)
			}

			if err := tt.apply(t, old); !errors.Is(err, schema.ErrBinaryBelowMinimum) {
				t.Fatalf("swap = %v, want ErrBinaryBelowMinimum", err)
			}

			requireApplied(t, current, 9999)

			if _, err := current.GetNamespace(t.Context(), ns.Name); err != nil {
				t.Fatalf("live namespace after the refused swap: %v", err)
			}
		})
	}
}

// TestRestoreAreaSwapOnBinaryBelowMinimum proves that the swap step of a
// restore operation is refused in the swap transaction on a binary below the
// minimum of the live schema, with no change: the area stays, the step is
// not recorded, and the live schema and its minimum stay as they were. The
// old handle stands for the old binary, and a second handle stands for the
// new binary that raises the minimum.
func TestRestoreAreaSwapOnBinaryBelowMinimum(t *testing.T) {
	t.Parallel()

	url := membershipURL(t)
	old := openAt(t, url, "v0.4.0")
	ns := createAcme(t, old)
	staged := storetest.StageRestoreArea(t, old)

	current := openAt(t, url, "v0.5.0")
	applyContract(t, current, "v0.5.0")

	if _, err := old.ApplyRestoreArea(t.Context(), staged.Swap, staged.Area, false); !errors.Is(
		err, schema.ErrBinaryBelowMinimum,
	) {
		t.Fatalf("swap of the restore area = %v, want ErrBinaryBelowMinimum", err)
	}

	areas, err := current.ListRestoreAreas(t.Context())
	if err != nil || !slices.Contains(areas, staged.Area) {
		t.Fatalf("restore areas = %v, %v; want %s kept", areas, err, staged.Area)
	}

	requireApplied(t, current, 9999)

	if _, err := current.GetNamespace(t.Context(), ns.Name); err != nil {
		t.Fatalf("live namespace after the refused swap: %v", err)
	}
}
