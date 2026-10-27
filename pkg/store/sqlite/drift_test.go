package sqlite_test

import (
	"errors"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// driftBackend returns the backend of the drift suite over a fresh store. The
// namespaces it makes have failover version 1.
func driftBackend(t *testing.T) storetest.DriftBackend {
	t.Helper()

	_, b := openDrift(t)

	return b
}

// openDrift returns a fresh store and its backend for the drift suite.
func openDrift(t *testing.T) (*sqlite.Store, storetest.DriftBackend) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return s, storetest.DriftBackend{
		Drift: s.DriftResults(),
		Jobs:  s.Jobs(),
		Namespace: func(t *testing.T, name string) namespace.Fence {
			t.Helper()

			return ensureNamespace(t, s, namespace.Name(name))
		},
		SetJob: func(t *testing.T, id job.ID, state job.State, createdMs, updatedMs int64) {
			t.Helper()

			setJobRow(t, path, jobTimes{id: id, state: state, createdMs: createdMs, updatedMs: updatedMs})
		},
	}
}

// jobTimes is the state and the times of one job row, in Unix milliseconds.
type jobTimes struct {
	id                   job.ID
	state                job.State
	createdMs, updatedMs int64
}

// setJobRow sets the state and the times of the job id in the database at path
// with direct SQL.
func setJobRow(t *testing.T, path string, row jobTimes) {
	t.Helper()

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	_, err = conn.ExecContext(t.Context(),
		`UPDATE jobs SET state = ?, created_at_ms = ?, updated_at_ms = ? WHERE id = ?`,
		string(row.state), row.createdMs, row.updatedMs, string(row.id))
	if err != nil {
		t.Fatalf("set job %s: %v", row.id, err)
	}
}

var _ storetest.DriftSeeder = (*backupStore)(nil)

// SeedDriftResult implements storetest.DriftSeeder.
func (s *backupStore) SeedDriftResult(t *testing.T, ns namespace.Namespace) {
	t.Helper()

	b := storetest.DriftBackend{
		Drift: s.DriftResults(), Jobs: s.Jobs(),
		Namespace: func(*testing.T, string) namespace.Fence { return ns.Fence() },
		SetJob: func(t *testing.T, id job.ID, state job.State, createdMs, updatedMs int64) {
			t.Helper()

			setJobRow(t, s.path, jobTimes{id: id, state: state, createdMs: createdMs, updatedMs: updatedMs})
		},
	}

	unit := graph.Unit{Namespace: ns.Name, Tool: "opentofu", ID: "wsp-drift"}
	if _, err := b.Drift.Record(t.Context(), b.FixtureCheck(t, unit, "aws_s3_bucket.logs"), ns.Fence()); err != nil {
		t.Fatalf("record the drift result: %v", err)
	}
}

func TestDriftConformance(t *testing.T) {
	t.Parallel()

	storetest.RunDrift(t, driftBackend)
}

// TestPurgeDeletesTheDriftResult proves that the purge of a unit removes its
// latest drift result in the same transaction, and leaves the result of another
// unit alone.
func TestPurgeDeletesTheDriftResult(t *testing.T) {
	t.Parallel()

	s, b := openDrift(t)
	fence := ensureNamespace(t, s, "acme")
	gone := graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "ws-gone"}
	kept := graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "ws-kept"}

	for _, u := range []graph.Unit{gone, kept} {
		contextUnit(t, s, u)

		if _, err := b.Drift.Record(t.Context(), b.FixtureCheck(t, u, "aws_s3_bucket.logs"), fence); err != nil {
			t.Fatalf("record for %s: %v", u.ID, err)
		}
	}

	contextPurge(t, s, gone)

	if _, err := b.Drift.GetLatest(t.Context(), gone); !errors.Is(err, drift.ErrNotFound) {
		t.Errorf("GetLatest of the purged unit = %v, want ErrNotFound", err)
	}

	if got, err := b.Drift.GetLatest(t.Context(), kept); err != nil || got.Status != drift.StatusDrifted {
		t.Errorf("GetLatest of the other unit = %+v, %v; want its drifted result", got, err)
	}
}
