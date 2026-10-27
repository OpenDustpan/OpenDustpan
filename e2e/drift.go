//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// errDriftSeam reports a drift record on a server that the harness cannot reach
// through its store.
var errDriftSeam = errors.New("a drift record needs the in-process server")

// RecordDrift records a drift result of the OpenTofu workspace unitID with the
// given addresses, as the latest result of the drift check job jobID. No runner
// reports drift yet, so a scenario that reads drift writes the result through
// the store of the in-process server. The job comes from the API. The step
// sets its row to succeeded with direct SQL, because the store accepts a result
// only for a drift check job that succeeded and a time inside the run of that
// job. The binary server holds its database open, so the step fails there.
func (e *Env) RecordDrift(ctx context.Context, tb testing.TB, unitID, jobID string, addresses ...string) {
	tb.Helper()

	if e.binPath != "" || e.inprocess == nil {
		tb.Fatalf("%v", errDriftSeam)
	}

	results, err := driftResultsOf(e.inprocess)
	if err != nil {
		tb.Fatalf("drift store: %v", err)
	}

	ns := namespace.Name(Namespace)

	n, err := e.inprocess.GetNamespace(ctx, ns)
	if err != nil {
		tb.Fatalf("namespace %s: %v", ns, err)
	}

	now := time.Now()
	e.setJobSucceeded(ctx, tb, jobID, now.Add(-2*time.Minute), now.Add(-time.Minute))

	resources := make([]drift.Resource, len(addresses))
	for i, address := range addresses {
		resources[i] = drift.Resource{Address: address, Kind: drift.KindChanged}
	}

	_, err = results.Record(ctx, drift.Result{
		CheckedAt: now.Add(-90 * time.Second),
		Unit:      graph.Unit{Namespace: ns, Tool: "opentofu", ID: unitID},
		JobID:     job.ID(jobID),
		Resources: resources,
		Truncated: false,
	}, n.Fence())
	if err != nil {
		tb.Fatalf("record the drift result of %s: %v", unitID, err)
	}
}

// driftResultsOf returns the drift store of st.
//
//nolint:ireturn // The harness takes the port of whichever store the run uses.
func driftResultsOf(st any) (drift.Store, error) {
	switch s := st.(type) {
	case *sqlite.Store:
		return s.DriftResults(), nil
	case *postgres.Store:
		return s.DriftResults(), nil
	default:
		return nil, fmt.Errorf("%w: the store %T keeps no drift results", errDriftSeam, st)
	}
}

// setJobSucceeded sets the state succeeded and the times of the job id in the
// database of the in-process server with direct SQL.
func (e *Env) setJobSucceeded(ctx context.Context, tb testing.TB, id string, created, updated time.Time) {
	tb.Helper()

	switch e.store {
	case StoreSQLite:
		query := url.Values{}
		query.Add("_pragma", "busy_timeout(10000)")

		dsn := (&url.URL{
			Scheme: "file", Path: strings.TrimPrefix(e.dbURL, "sqlite:"), RawQuery: query.Encode(),
		}).String()

		conn, err := sql.Open("sqlite", dsn)
		if err != nil {
			tb.Fatalf("open the database: %v", err)
		}

		defer func() {
			if closeErr := conn.Close(); closeErr != nil {
				tb.Errorf("close the database: %v", closeErr)
			}
		}()

		_, err = conn.ExecContext(ctx, `UPDATE jobs SET state = ?, created_at_ms = ?, updated_at_ms = ? WHERE id = ?`,
			string(job.StateSucceeded), created.UnixMilli(), updated.UnixMilli(), id)
		if err != nil {
			tb.Fatalf("set job %s: %v", id, err)
		}
	case StorePostgres:
		conn, err := pgx.Connect(ctx, e.dbURL)
		if err != nil {
			tb.Fatalf("connect to the database: %v", err)
		}

		defer func() {
			if closeErr := conn.Close(ctx); closeErr != nil {
				tb.Errorf("close the database: %v", closeErr)
			}
		}()

		_, err = conn.Exec(ctx, `UPDATE jobs SET state = $1, created_at_ms = $2, updated_at_ms = $3 WHERE id = $4`,
			string(job.StateSucceeded), created.UnixMilli(), updated.UnixMilli(), id)
		if err != nil {
			tb.Fatalf("set job %s: %v", id, err)
		}
	default:
		tb.Fatalf("unknown store: %s", e.store)
	}
}
