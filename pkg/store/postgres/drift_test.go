package postgres_test

import (
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// driftBackend returns the backend factory of the drift suite over fresh
// schemas of the database at base. The namespaces it makes are homed with
// failover version 1.
func driftBackend(base string) func(t *testing.T) storetest.DriftBackend {
	return func(t *testing.T) storetest.DriftBackend {
		t.Helper()

		_, b := openDrift(t, base)

		return b
	}
}

// openDrift returns a store in a fresh schema and its backend for the drift
// suite.
func openDrift(t *testing.T, base string) (*postgres.Store, storetest.DriftBackend) {
	t.Helper()

	schemaURL := isolatedURL(t, base)
	s := openURL(t, schemaURL)

	return s, newDriftBackend(s, schemaURL)
}

func newDriftBackend(s *postgres.Store, schemaURL string) storetest.DriftBackend {
	return storetest.DriftBackend{
		Drift: s.DriftResults(),
		Jobs:  s.Jobs(),
		Namespace: func(t *testing.T, name string) namespace.Fence {
			t.Helper()

			return ensureNamespace(t, s, namespace.Name(name))
		},
		SetJob: func(t *testing.T, id job.ID, state job.State, createdMs, updatedMs int64) {
			t.Helper()

			setJobRow(t, schemaURL, jobTimes{id: id, state: state, createdMs: createdMs, updatedMs: updatedMs})
		},
	}
}

// jobTimes is the state and the times of one job row, in Unix milliseconds.
type jobTimes struct {
	id                   job.ID
	state                job.State
	createdMs, updatedMs int64
}

// setJobRow sets the state and the times of the job id in the schema that
// schemaURL names, with direct SQL.
func setJobRow(t *testing.T, schemaURL string, row jobTimes) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	_, err = conn.Exec(t.Context(),
		`UPDATE jobs SET state = $1, created_at_ms = $2, updated_at_ms = $3 WHERE id = $4`,
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

			setJobRow(t, s.url, jobTimes{id: id, state: state, createdMs: createdMs, updatedMs: updatedMs})
		},
	}

	unit := graph.Unit{Namespace: ns.Name, Tool: "opentofu", ID: "wsp-drift"}
	if _, err := b.Drift.Record(t.Context(), b.FixtureCheck(t, unit, "aws_s3_bucket.logs"), ns.Fence()); err != nil {
		t.Fatalf("record the drift result: %v", err)
	}
}

func TestDriftConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunDrift(t, driftBackend(base))
}

// TestPurgeDeletesTheDriftResult proves that the purge of a unit removes its
// latest drift result in the same transaction, and leaves the result of another
// unit alone.
func TestPurgeDeletesTheDriftResult(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL suite", envURL)
	}

	s, b := openDrift(t, base)
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
