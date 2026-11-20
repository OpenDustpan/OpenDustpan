package sqlite_test

import (
	"errors"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

func TestGateConformance(t *testing.T) {
	t.Parallel()

	storetest.RunGate(t, func(t *testing.T) storetest.GateStore {
		t.Helper()

		return open(t)
	})
}

// TestRecordsGetWrapsStoreError proves that a read that fails with a driver
// error returns an error that wraps gate.ErrUnavailable and not
// gate.ErrNotFound, so that a store outage does not read as a missing record.
func TestRecordsGetWrapsStoreError(t *testing.T) {
	t.Parallel()

	s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "records.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	records := s.Records()

	if closeErr := s.Close(); closeErr != nil {
		t.Fatalf("close store: %v", closeErr)
	}

	_, err = records.Get(t.Context(), "acme", "gdr-a1b2c3d4e5f6g7h8i9j0")
	if !errors.Is(err, gate.ErrUnavailable) {
		t.Fatalf("Get on a closed store error = %v, want gate.ErrUnavailable", err)
	}

	if errors.Is(err, gate.ErrNotFound) {
		t.Errorf("Get on a closed store error = %v, must not wrap gate.ErrNotFound", err)
	}
}
