package postgres

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// stateCloseTx runs the close of the jobs that wait for a state version over
// the queries of one state write. The write helpers of the store give their
// callback the queries only, and the close runs no hand-written query, so
// this type carries the four calls that the close makes.
type stateCloseTx struct {
	q *db.Queries
}

var _ jobstore.CloseTx = stateCloseTx{q: nil}

// Now implements jobstore.CloseTx.
func (t stateCloseTx) Now(ctx context.Context) (time.Time, error) {
	return databaseNow(ctx, t.q)
}

// Awaiting implements jobstore.CloseTx.
func (t stateCloseTx) Awaiting(ctx context.Context, ns namespace.Name, unitID string) ([]jobstore.Row, error) {
	return awaitingRows(ctx, t.q, ns, unitID)
}

// UpdateJob implements jobstore.CloseTx.
func (t stateCloseTx) UpdateJob(ctx context.Context, r jobstore.Row, prev int64) (bool, error) {
	return updateJobRow(ctx, t.q, r, prev)
}

// StartOperation implements jobstore.CloseTx.
func (t stateCloseTx) StartOperation(ctx context.Context, req *durable.StartRequest) (string, error) {
	return startDurableOperation(ctx, t.q, req)
}

// databaseNow returns the time on the clock of the database, in the
// transaction of q.
func databaseNow(ctx context.Context, q *db.Queries) (time.Time, error) {
	ms, err := storeNow(ctx, q)
	if err != nil {
		return time.Time{}, err
	}

	return time.UnixMilli(ms).UTC(), nil
}

// awaitingRows returns the jobs of the unit unitID of the namespace ns that
// wait for a state version, oldest first, in the transaction of q.
func awaitingRows(ctx context.Context, q *db.Queries, ns namespace.Name, unitID string) ([]jobstore.Row, error) {
	rows, err := q.ListAwaitingJobsOfUnit(
		ctx,
		db.ListAwaitingJobsOfUnitParams{Namespace: ns.String(), UnitID: unitID},
	)
	if err != nil {
		return nil, fmt.Errorf("select awaiting jobs: %w", err)
	}

	out := make([]jobstore.Row, len(rows))
	for i := range rows {
		out[i] = jobRow(db.GetJobRow(rows[i]))
	}

	return out, nil
}

// updateJobRow replaces the row of version prev with r, in the transaction of
// q, and reports false when the stored version is not prev.
func updateJobRow(ctx context.Context, q *db.Queries, r jobstore.Row, prev int64) (bool, error) {
	n, err := q.UpdateJob(ctx, db.UpdateJobParams{
		State: string(r.State), Wait: string(r.Wait), CurrentPhase: int64(r.Phase), CurrentChanges: r.Changes,
		CurrentOperation: r.Operation, Version: r.Version, Body: r.Body, UpdatedAtMs: r.UpdatedMs,
		StateChangedAtMs: r.StateChangedMs, ID: string(r.ID), PrevVersion: prev,
	})
	if err != nil {
		return false, jobIndexError("update job", err)
	}

	return n == 1, nil
}

// startDurableOperation starts the durable operation of req, in the
// transaction of q, and returns its ID.
func startDurableOperation(ctx context.Context, q *db.Queries, req *durable.StartRequest) (string, error) {
	op, _, err := startOperation(ctx, q, *req)
	if err != nil {
		return "", err
	}

	return string(op.ID), nil
}
