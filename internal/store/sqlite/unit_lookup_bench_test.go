package sqlite_test

import (
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// BenchmarkUnitLookup measures the read of one unit of a namespace of 20,000
// units, by a list and a scan and by the unit ID.
func BenchmarkUnitLookup(b *testing.B) {
	s, err := sqlite.Open(b.Context(), filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}

	b.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			b.Errorf("close store: %v", closeErr)
		}
	})

	storetest.BenchUnitLookup(b, s)
}
