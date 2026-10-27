package sqlite

import (
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// openBench opens a new database as a binary of version v0.5.0, and applies a
// contract migration with minimum when it is not empty.
func openBench(b *testing.B, minimum string) *Store {
	b.Helper()

	s, err := Open(b.Context(), filepath.Join(b.TempDir(), "dustpan.db"), WithBinaryVersion("v0.5.0"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}

	b.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			b.Errorf("close store: %v", closeErr)
		}
	})

	if minimum != "" {
		if applyErr := s.apply(b.Context(), contractMigration(minimum)); applyErr != nil {
			b.Fatalf("apply contract migration: %v", applyErr)
		}
	}

	return s
}

// BenchmarkWriteCheck records the cost that the minimum binary version adds
// to the check that every write transaction runs: the state read and the
// minimum read, in one statement, with an empty transaction body. "state
// only" is the query from before the minimum joined the statement. The
// "minimum" cases run the current statement, with no minimum in the schema
// and with one that the binary meets.
func BenchmarkWriteCheck(b *testing.B) {
	b.Run("state only", func(b *testing.B) {
		s := openBench(b, "")

		b.ReportAllocs()

		for b.Loop() {
			err := s.inControlTx(b.Context(), func(q *db.Queries) error {
				_, readErr := q.GetInstallationState(b.Context())

				return readErr
			})
			if err != nil {
				b.Fatalf("check: %v", err)
			}
		}
	})

	for _, tc := range []struct{ name, minimum string }{
		{name: "state and minimum, none set", minimum: ""},
		{name: "state and minimum, one set", minimum: "v0.5.0"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			s := openBench(b, tc.minimum)

			b.ReportAllocs()

			for b.Loop() {
				if err := s.inTx(b.Context(), func(*db.Queries) error { return nil }); err != nil {
					b.Fatalf("check: %v", err)
				}
			}
		})
	}
}
