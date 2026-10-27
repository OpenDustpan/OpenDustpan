package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// durableIDPrefix starts the ID of each durable operation.
const durableIDPrefix = "op-"

// DurableOperations implements durable.Store on the database of a Store. It is a
// type of its own because the method names of the port, such as List, are
// already taken on Store by other ports.
//
// Every time comes from the database clock with clock_timestamp(), read when
// the statement runs: the adapter reads no time in Go. So neither the clock
// of an instance nor the start of a long transaction moves a decision. A
// write of a holder locks the row of its operation, reads the clock, and
// checks the claim token in the transaction of the write.
type DurableOperations struct {
	store *Store
}

var _ durable.Store = (*DurableOperations)(nil)

// A restore cancels through the guard of its swap step, so the store must
// have it.
var _ durable.StepCanceler = (*DurableOperations)(nil)

// DurableOperations returns the store of durable operations on the database
// of s.
func (s *Store) DurableOperations() *DurableOperations {
	return &DurableOperations{store: s}
}

// DurableOperationsPort answers the durable operations as the port, for
// store agnostic discovery. Direct callers use DurableOperations.
//
//nolint:ireturn // Store-agnostic discovery takes the port.
func (s *Store) DurableOperationsPort() durable.Store {
	return s.DurableOperations()
}

// Start implements durable.Store.
func (d *DurableOperations) Start(ctx context.Context, req durable.StartRequest) (durable.Operation, bool, error) {
	if err := req.Validate(); err != nil {
		return durable.Operation{}, false, err
	}

	var (
		op      durable.Operation
		created bool
	)

	err := d.store.inControlTx(ctx, func(q *db.Queries) error {
		var startErr error

		op, created, startErr = startOperation(ctx, q, req)

		return startErr
	})
	if err != nil {
		return durable.Operation{}, false, fmt.Errorf("start durable operation: %w", err)
	}

	return op, created, nil
}

// startOperation creates the operation of req in the transaction of q, or
// reads the operation that req.Key already names. A domain write that starts
// an operation calls it in its own transaction. A concurrent start with the
// same key waits for the first insert, and then reads its row.
func startOperation(ctx context.Context, q *db.Queries, req durable.StartRequest) (durable.Operation, bool, error) {
	if err := req.Validate(); err != nil {
		return durable.Operation{}, false, err
	}

	row, err := q.InsertDurableOperation(ctx, db.InsertDurableOperationParams{
		ID: ident.NewID(durableIDPrefix), StartKey: req.Key, Kind: string(req.Kind), Input: nonNilBytes(req.Input),
		Version: int64(req.Version),
	})
	if err == nil {
		return operationFromRow(row, nil), true, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return durable.Operation{}, false, fmt.Errorf("insert operation: %w", err)
	}

	row, err = q.GetDurableOperationByKey(ctx, req.Key)
	if err != nil {
		return durable.Operation{}, false, fmt.Errorf("read operation of the start key: %w", err)
	}

	if row.Kind != string(req.Kind) {
		return durable.Operation{}, false, durable.ErrKeyConflict
	}

	op, err := withSteps(ctx, q, row)
	if err != nil {
		return durable.Operation{}, false, err
	}

	return op, false, nil
}

// Get implements durable.Store.
func (d *DurableOperations) Get(ctx context.Context, id durable.ID) (durable.Operation, error) {
	var op durable.Operation

	err := d.store.inSnapshotTx(ctx, func(q *db.Queries) error {
		row, err := q.GetDurableOperation(ctx, string(id))
		if errors.Is(err, pgx.ErrNoRows) {
			return durable.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("read operation: %w", err)
		}

		op, err = withSteps(ctx, q, row)

		return err
	})
	if err != nil {
		return durable.Operation{}, fmt.Errorf("get durable operation %s: %w", id, err)
	}

	return op, nil
}

// List implements durable.Store.
func (d *DurableOperations) List(ctx context.Context, f durable.Filter) ([]durable.Operation, error) {
	states := make([]string, 0, len(f.States))
	for _, st := range f.States {
		states = append(states, string(st))
	}

	out := []durable.Operation{}

	err := d.store.inSnapshotTx(ctx, func(q *db.Queries) error {
		rows, err := q.ListDurableOperations(ctx, db.ListDurableOperationsParams{
			Kind: string(f.Kind), States: states, MaxRows: int64(max(f.Limit, 0)),
		})
		if err != nil {
			return fmt.Errorf("list operations: %w", err)
		}

		ids := make([]string, 0, len(rows))
		for i := range rows {
			ids = append(ids, rows[i].ID)
		}

		steps, err := q.ListDurableSteps(ctx, ids)
		if err != nil {
			return fmt.Errorf("list steps: %w", err)
		}

		for i := range rows {
			out = append(out, operationFromRow(rows[i], steps))
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list durable operations: %w", err)
	}

	return out, nil
}

// Claim implements durable.Store. One short transaction first ends the
// expired operations of the kinds that have a cancel request (unless
// req.TakeCanceled is true), then claims
// the oldest operation that can run. It commits before Claim returns. A
// row that a concurrent claim holds is skipped, so the loser of a race gets
// ErrNoWork.
func (d *DurableOperations) Claim(ctx context.Context, req durable.ClaimRequest) (durable.Operation, error) {
	if err := req.Validate(); err != nil {
		return durable.Operation{}, err
	}

	kinds, maxVersions := claimLimits(req)

	var (
		op    durable.Operation
		found bool
	)

	// No work is not an error of the transaction: the end of the canceled
	// operations commits also when no operation can run.
	err := d.store.inControlTx(ctx, func(q *db.Queries) error {
		if !req.TakeCanceled {
			ended := db.EndExpiredCanceledDurableOperationsParams{Kinds: kinds, MaxVersions: maxVersions}
			if err := q.EndExpiredCanceledDurableOperations(ctx, ended); err != nil {
				return fmt.Errorf("end canceled operations: %w", err)
			}
		}

		row, err := q.ClaimDurableOperation(ctx, db.ClaimDurableOperationParams{
			Kinds: kinds, MaxVersions: maxVersions, Owner: req.Owner, LeaseMs: req.Lease.Milliseconds(),
			TakeCanceled: req.TakeCanceled,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("claim operation: %w", err)
		}

		found = true
		op, err = withSteps(ctx, q, row)

		return err
	})
	if err != nil {
		return durable.Operation{}, fmt.Errorf("claim durable operation: %w", err)
	}

	if !found {
		return durable.Operation{}, durable.ErrNoWork
	}

	return op, nil
}

// claimLimits returns the kinds of req and the version limit of each kind,
// as two lists of one length. A kind with no limit gets the largest
// version, so it takes every operation of its kind.
func claimLimits(req durable.ClaimRequest) ([]string, []int64) {
	kinds := make([]string, 0, len(req.Kinds))
	limits := make([]int64, 0, len(req.Kinds))

	for _, k := range req.Kinds {
		limit := int64(math.MaxInt64)
		if v, ok := req.MaxVersions[k]; ok {
			limit = int64(v)
		}

		kinds, limits = append(kinds, string(k)), append(limits, limit)
	}

	return kinds, limits
}

// Extend implements durable.Store.
func (d *DurableOperations) Extend(ctx context.Context, c durable.Claim, lease time.Duration) (durable.Claim, error) {
	if lease <= 0 {
		return durable.Claim{}, durable.ErrInvalid
	}

	var out durable.Claim

	err := d.store.inControlTx(ctx, func(q *db.Queries) error {
		row, now, err := lockLive(ctx, q, c.Operation, c.Token)
		if err != nil {
			return err
		}

		if row.CancelRequested {
			return durable.ErrCanceled
		}

		expires := now + lease.Milliseconds()

		params := db.ExtendDurableClaimParams{ExpiresAtMs: expires, NowMs: now, ID: row.ID}
		if err := q.ExtendDurableClaim(ctx, params); err != nil {
			return fmt.Errorf("extend claim: %w", err)
		}

		row.ClaimExpiresAtMs = pgtype.Int8{Int64: expires, Valid: true}
		out = claimFromRow(row)

		return nil
	})
	if err != nil {
		return durable.Claim{}, fmt.Errorf("extend durable claim: %w", err)
	}

	return out, nil
}

// RecordStep implements durable.Store.
func (d *DurableOperations) RecordStep(ctx context.Context, f durable.StepFence, result []byte) error {
	err := d.store.inControlTx(ctx, func(q *db.Queries) error {
		return recordStep(ctx, q, f, result)
	})
	if err != nil {
		return fmt.Errorf("record durable step %q: %w", f.Step, err)
	}

	return nil
}

// recordStep checks f and records its step with result in the transaction
// of q, with the rules of durable.Store.RecordStep. A domain write that
// carries a StepFence calls it in its own transaction, so the write and the
// step record commit together. The time of the expiry check is the database
// clock when recordStep runs, not when the transaction started.
func recordStep(ctx context.Context, q *db.Queries, f durable.StepFence, result []byte) error {
	row, now, err := lockLive(ctx, q, f.Operation, f.Token)
	if err != nil {
		return err
	}

	if row.CancelRequested {
		return durable.ErrCanceled
	}

	done, err := q.DurableStepExists(ctx, db.DurableStepExistsParams{OperationID: row.ID, Name: f.Step})
	if err != nil {
		return fmt.Errorf("read step: %w", err)
	}

	if done {
		return durable.ErrStepDone
	}

	step := db.InsertDurableStepParams{OperationID: row.ID, Name: f.Step, Result: nonNilBytes(result), NowMs: now}
	if err := q.InsertDurableStep(ctx, step); err != nil {
		return fmt.Errorf("insert step: %w", err)
	}

	if err := q.TouchDurableOperation(ctx, db.TouchDurableOperationParams{NowMs: now, ID: row.ID}); err != nil {
		return fmt.Errorf("touch operation: %w", err)
	}

	return nil
}

// Complete implements durable.Store.
func (d *DurableOperations) Complete(ctx context.Context, c durable.Claim) error {
	err := d.end(ctx, c, completeEnd)
	if err != nil {
		return fmt.Errorf("complete durable operation %s: %w", c.Operation, err)
	}

	return nil
}

// Fail implements durable.Store.
func (d *DurableOperations) Fail(ctx context.Context, c durable.Claim, f durable.Failure) error {
	err := d.end(ctx, c, failEnd(f))
	if err != nil {
		return fmt.Errorf("fail durable operation %s: %w", c.Operation, err)
	}

	return nil
}

// Release implements durable.Store.
func (d *DurableOperations) Release(ctx context.Context, c durable.Claim) error {
	err := d.end(ctx, c, releaseEnd)
	if err != nil {
		return fmt.Errorf("release durable operation %s: %w", c.Operation, err)
	}

	return nil
}

// GiveBack implements durable.Store.
func (d *DurableOperations) GiveBack(ctx context.Context, c durable.Claim) error {
	err := d.end(ctx, c, giveBackEnd)
	if err != nil {
		return fmt.Errorf("give back durable operation %s: %w", c.Operation, err)
	}

	return nil
}

// Cancel implements durable.Store.
func (d *DurableOperations) Cancel(ctx context.Context, id durable.ID) error {
	err := d.store.inControlTx(ctx, func(q *db.Queries) error {
		return cancelDurable(ctx, q, id)
	})
	if err != nil {
		return fmt.Errorf("cancel durable operation %s: %w", id, err)
	}

	return nil
}

// CancelBefore implements durable.StepCanceler. It locks the operation row
// first, as recordStep does, so a step record and the cancel run one after
// the other and only one of them wins.
func (d *DurableOperations) CancelBefore(ctx context.Context, id durable.ID, step string) error {
	if step == "" {
		return fmt.Errorf("cancel durable operation %s before a step with no name: %w", id, durable.ErrInvalid)
	}

	err := d.store.inControlTx(ctx, func(q *db.Queries) error {
		row, err := q.LockDurableOperation(ctx, string(id))
		if errors.Is(err, pgx.ErrNoRows) {
			return durable.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("lock operation: %w", err)
		}

		if row.State == string(durable.StateSucceeded) || row.State == string(durable.StateFailed) {
			return durable.ErrFinished
		}

		done, err := q.DurableStepExists(ctx, db.DurableStepExistsParams{OperationID: row.ID, Name: step})
		if err != nil {
			return fmt.Errorf("read step: %w", err)
		}

		if done {
			return durable.ErrStepDone
		}

		return cancelDurable(ctx, q, id)
	})
	if err != nil {
		return fmt.Errorf("cancel durable operation %s before step %q: %w", id, step, err)
	}

	return nil
}

// cancelDurable applies the rules of durable.Store.Cancel to the operation id
// in the transaction of q. A domain write that cancels an operation in its
// own transaction calls it.
func cancelDurable(ctx context.Context, q *db.Queries, id durable.ID) error {
	row, err := q.LockDurableOperation(ctx, string(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return durable.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("lock operation: %w", err)
	}

	params := db.CancelDurableOperationParams{
		State: row.State, CancelRequested: row.CancelRequested, NowMs: 0, ID: row.ID,
	}

	switch durable.State(row.State) {
	case durable.StatePending:
		params.State = string(durable.StateCanceled)
	case durable.StateRunning:
		params.CancelRequested = true
	case durable.StateSucceeded, durable.StateFailed:
		return durable.ErrFinished
	case durable.StateCanceled:
		return nil
	default:
		return fmt.Errorf("%w: state %q", errUnknownDurableState, row.State)
	}

	params.NowMs, err = q.DatabaseNowMs(ctx)
	if err != nil {
		return fmt.Errorf("read the database clock: %w", err)
	}

	if err := q.CancelDurableOperation(ctx, params); err != nil {
		return fmt.Errorf("cancel operation: %w", err)
	}

	return nil
}

// end checks that c is the live claim of its operation and ends the claim
// with the params that next returns. When next returns an error, end
// changes nothing.
func (d *DurableOperations) end(ctx context.Context, c durable.Claim, next endRule) error {
	return d.store.inControlTx(ctx, func(q *db.Queries) error {
		return endClaim(ctx, q, c, next)
	})
}

// endRule returns the end of the claim of row at now, or the error that
// stops the end.
type endRule func(row db.DurableOperation, now int64) (db.EndDurableClaimParams, error)

// endClaim checks that c is the live claim of its operation in the
// transaction of q, and ends the claim with the params that next returns.
// When next returns an error, endClaim changes nothing. A domain write that
// ends an operation in its own transaction calls it.
func endClaim(ctx context.Context, q *db.Queries, c durable.Claim, next endRule) error {
	row, now, err := lockLive(ctx, q, c.Operation, c.Token)
	if err != nil {
		return err
	}

	params, err := next(row, now)
	if err != nil {
		return err
	}

	if err := q.EndDurableClaim(ctx, params); err != nil {
		return fmt.Errorf("end claim: %w", err)
	}

	return nil
}

// completeEnd is the rule of durable.Store.Complete.
func completeEnd(row db.DurableOperation, now int64) (db.EndDurableClaimParams, error) {
	if row.CancelRequested {
		return db.EndDurableClaimParams{}, durable.ErrCanceled
	}

	return endParams(row, durable.StateSucceeded, row.LastError, row.NotBeforeMs, now), nil
}

// failEnd returns the rule of durable.Store.Fail with f.
func failEnd(f durable.Failure) endRule {
	return func(row db.DurableOperation, now int64) (db.EndDurableClaimParams, error) {
		switch {
		case row.CancelRequested:
			return endParams(row, durable.StateCanceled, row.LastError, row.NotBeforeMs, now), nil
		case f.Final:
			return failedAttempt(endParams(row, durable.StateFailed, f.Message, row.NotBeforeMs, now)), nil
		default:
			next := now + f.RetryAfter.Milliseconds()

			return failedAttempt(endParams(row, durable.StatePending, f.Message, next, now)), nil
		}
	}
}

// releaseEnd is the rule of durable.Store.Release.
func releaseEnd(row db.DurableOperation, now int64) (db.EndDurableClaimParams, error) {
	if row.CancelRequested {
		return endParams(row, durable.StateCanceled, row.LastError, row.NotBeforeMs, now), nil
	}

	return endParams(row, durable.StatePending, row.LastError, now, now), nil
}

// giveBackEnd is the rule of durable.Store.GiveBack.
func giveBackEnd(row db.DurableOperation, now int64) (db.EndDurableClaimParams, error) {
	if row.CancelRequested {
		return db.EndDurableClaimParams{}, durable.ErrCanceled
	}

	return releaseEnd(row, now)
}

// endParams returns the end of the claim of row with the next state, the
// last error and the earliest next claim, at now. It adds no failure.
func endParams(
	row db.DurableOperation, state durable.State, lastError string, notBeforeMs, now int64,
) db.EndDurableClaimParams {
	return db.EndDurableClaimParams{
		State: string(state), LastError: lastError, AddFailures: 0, NotBeforeMs: notBeforeMs, NowMs: now, ID: row.ID,
	}
}

// failedAttempt returns p with one more failure: the end of a failed
// attempt. A cancel that Fail finds ends the operation without one.
func failedAttempt(p db.EndDurableClaimParams) db.EndDurableClaimParams {
	p.AddFailures = 1

	return p
}

// errUnknownDurableState reports a stored operation state that the adapter
// does not know.
var errUnknownDurableState = errors.New("unknown durable operation state")

// lockLive locks the row of the operation id and checks that token is its
// live claim on the database clock. It returns the row and the time of the
// check in Unix milliseconds. The clock is read after the lock, so a wait
// for the lock cannot make an expired claim look live. It returns
// durable.ErrClaimLost when the operation is unknown or not running, when
// the claim expired, or when token is not the live token.
func lockLive(ctx context.Context, q *db.Queries, id durable.ID, token int64) (db.DurableOperation, int64, error) {
	row, err := q.LockDurableOperation(ctx, string(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return db.DurableOperation{}, 0, durable.ErrClaimLost
	}

	if err != nil {
		return db.DurableOperation{}, 0, fmt.Errorf("lock operation: %w", err)
	}

	now, err := q.DatabaseNowMs(ctx)
	if err != nil {
		return db.DurableOperation{}, 0, fmt.Errorf("read the database clock: %w", err)
	}

	if row.State != string(durable.StateRunning) || !row.ClaimExpiresAtMs.Valid ||
		row.ClaimExpiresAtMs.Int64 <= now || row.ClaimToken != token {
		return db.DurableOperation{}, 0, durable.ErrClaimLost
	}

	return row, now, nil
}

// withSteps reads the steps of row and returns the operation.
func withSteps(ctx context.Context, q *db.Queries, row db.DurableOperation) (durable.Operation, error) {
	steps, err := q.ListDurableSteps(ctx, []string{row.ID})
	if err != nil {
		return durable.Operation{}, fmt.Errorf("list steps: %w", err)
	}

	return operationFromRow(row, steps), nil
}

// operationFromRow returns the operation of row with the steps of steps that
// belong to it, in the order of steps.
func operationFromRow(row db.DurableOperation, steps []db.DurableStep) durable.Operation {
	op := durable.Operation{
		ID: durable.ID(row.ID), Key: row.StartKey, Kind: durable.Kind(row.Kind), Version: int(row.Version),
		Input: slices.Clone(row.Input),
		State: durable.State(row.State), Steps: nil, Attempt: int(row.Attempt), Failures: int(row.Failures),
		Expiries: int(row.Expiries), LastError: row.LastError,
		Claim: claimFromRow(row), CancelRequested: row.CancelRequested, NotBefore: codec.FromMillis(row.NotBeforeMs),
		CreatedAt: codec.FromMillis(row.CreatedAtMs), UpdatedAt: codec.FromMillis(row.UpdatedAtMs),
	}

	for i := range steps {
		if steps[i].OperationID != row.ID {
			continue
		}

		op.Steps = append(op.Steps, durable.StepRecord{
			Name: steps[i].Name, Result: slices.Clone(steps[i].Result), DoneAt: codec.FromMillis(steps[i].DoneAtMs),
		})
	}

	return op
}

// claimFromRow returns the claim of row, or the zero claim when no instance
// holds the operation.
func claimFromRow(row db.DurableOperation) durable.Claim {
	if !row.ClaimExpiresAtMs.Valid {
		return durable.Claim{Operation: "", Owner: "", Token: 0, ExpiresAt: time.Time{}}
	}

	return durable.Claim{
		Operation: durable.ID(row.ID), Owner: row.ClaimOwner.String, Token: row.ClaimToken,
		ExpiresAt: codec.FromMillis(row.ClaimExpiresAtMs.Int64),
	}
}

// nonNilBytes returns b, or an empty slice for nil: pgx writes a nil slice as
// NULL, and the byte columns of durable operations are NOT NULL.
func nonNilBytes(b []byte) []byte {
	if b == nil {
		return []byte{}
	}

	return b
}
