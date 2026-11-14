package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/driftstore"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// DriftResults returns the store of the latest drift result of each unit.
// Each write checks the installation state and the fence of its namespace in
// its own transaction.
//
//nolint:ireturn // Store-agnostic wiring takes the port.
func (s *Store) DriftResults() drift.Store {
	return driftResults{s: s}
}

// driftResults is drift.Store over the database of a Store.
type driftResults struct {
	s *Store
}

var _ drift.Store = driftResults{s: nil}

// Record implements drift.Store.
func (d driftResults) Record(ctx context.Context, r drift.Result, fence namespace.Fence) (drift.Transition, error) {
	// An error returns the zero transition, so that no caller reads a result
	// from it.
	var none drift.Transition

	if err := r.Validate(); err != nil {
		return none, fmt.Errorf("record drift result: %w", err)
	}

	// Defense in depth: the fence check of the transaction compares the
	// namespace of the fence with the namespace row. This check stays so that
	// the result of a unit of one namespace cannot be written under the fence
	// of another namespace. It does not rely on the caller.
	if r.Unit.Namespace != fence.Namespace {
		return none, fmt.Errorf("%w: result names %q, fence names %q", namespace.ErrFenced, r.Unit.Namespace,
			fence.Namespace)
	}

	var tr drift.Transition

	err := d.s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		var recErr error

		tr, recErr = driftstore.Record(ctx, driftTx{q: q}, &r)

		return recErr
	})
	if err != nil {
		return none, err
	}

	return tr, nil
}

// GetLatest implements drift.Store. A row that fails its checks is not served,
// and it reads as no result.
func (d driftResults) GetLatest(ctx context.Context, unit graph.Unit) (drift.Latest, error) {
	if err := unit.Validate(); err != nil {
		return drift.Latest{}, fmt.Errorf("%w: unit: %w", drift.ErrInvalid, err)
	}

	row, ok, err := driftTx{q: d.s.queries}.Latest(ctx, unit)
	if err != nil {
		return drift.Latest{}, err
	}

	if !ok {
		return drift.Latest{}, drift.ErrNotFound
	}

	latest, err := driftstore.Latest(&row)
	if err != nil {
		return drift.Latest{}, drift.ErrNotFound
	}

	// Defense in depth: the query matched the namespace, the tool and the unit
	// ID. This check stays so that a change of the query cannot serve the
	// result of another unit.
	if latest.Unit != unit {
		return drift.Latest{}, drift.ErrNotFound
	}

	return latest, nil
}

// DeleteLatest implements drift.Store.
func (d driftResults) DeleteLatest(ctx context.Context, unit graph.Unit, fence namespace.Fence) error {
	if err := unit.Validate(); err != nil {
		return fmt.Errorf("%w: unit: %w", drift.ErrInvalid, err)
	}

	// Defense in depth: see Record.
	if unit.Namespace != fence.Namespace {
		return fmt.Errorf("%w: unit names %q, fence names %q", namespace.ErrFenced, unit.Namespace, fence.Namespace)
	}

	return d.s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		err := q.DeleteDriftResult(ctx, db.DeleteDriftResultParams{
			UnitID: unit.ID, Namespace: unit.Namespace.String(),
		})
		if err != nil {
			return fmt.Errorf("delete drift result: %w", err)
		}

		return nil
	})
}

// driftTx is the driftstore.Tx over one PostgreSQL transaction or the database.
type driftTx struct {
	q *db.Queries
}

var _ driftstore.Tx = driftTx{q: nil}

// Job implements driftstore.Tx.
func (t driftTx) Job(ctx context.Context, id job.ID) (jobstore.Row, bool, error) {
	var none jobstore.Row

	row, err := t.q.GetJob(ctx, string(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("select job: %w", err)
	}

	return jobRow(row), true, nil
}

// Latest implements driftstore.Tx.
func (t driftTx) Latest(ctx context.Context, unit graph.Unit) (driftstore.Row, bool, error) {
	var none driftstore.Row

	row, err := t.q.GetDriftResult(ctx, db.GetDriftResultParams{
		Namespace: unit.Namespace.String(), UnitID: unit.ID, Tool: unit.Tool,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("select drift result: %w", err)
	}

	return driftstore.Row{
		Namespace: row.Namespace, Tool: row.Tool, UnitID: row.UnitID, JobID: row.JobID, Status: row.Status,
		CheckedAtMs: row.CheckedAtMs, Truncated: row.Truncated, ResourcesJSON: row.ResourcesJson,
	}, true, nil
}

// Put implements driftstore.Tx.
func (t driftTx) Put(ctx context.Context, r driftstore.Row) error {
	n, err := t.q.UpsertDriftResult(ctx, db.UpsertDriftResultParams{
		UnitID: r.UnitID, Namespace: r.Namespace, Tool: r.Tool, JobID: r.JobID, Status: r.Status,
		CheckedAtMs: r.CheckedAtMs, Truncated: r.Truncated, ResourcesJson: r.ResourcesJSON,
	})
	if err != nil {
		return fmt.Errorf("upsert drift result: %w", err)
	}

	if n == 0 {
		// The stored row is of the same job, or it is not older than r, or
		// the unit ID belongs to another namespace or tool. Read the row to
		// tell them apart.
		_, found, err := t.Latest(ctx, graph.Unit{Namespace: namespace.Name(r.Namespace), Tool: r.Tool, ID: r.UnitID})
		if err != nil {
			return err
		}

		if found {
			return drift.ErrStale
		}

		return fmt.Errorf("%w: unit ID %q belongs to another namespace or another tool", drift.ErrInvalid,
			r.UnitID)
	}

	return nil
}
