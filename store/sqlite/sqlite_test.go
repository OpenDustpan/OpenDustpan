package sqlite_test

import (
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/store/sqlite"
	"opendustpan.org/opendustpan/store/storetest"
)

func TestConformance(t *testing.T) {
	t.Parallel()

	storetest.Run(t, func(t *testing.T) storetest.Store {
		t.Helper()

		s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Errorf("close store: %v", err)
			}
		})

		return s
	})
}
