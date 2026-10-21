package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ durable.Store = (*DurableOperations)(nil)

// DurableOperations is the store of durable operations on the database of a
// Store. It implements durable.Store. It shares the connection, the lock of
// the restore swap and the step transaction bound of its Store.
type DurableOperations struct {
	store *Store
}

// DurableOperations returns the store of durable operations on s.
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

// DefaultStepTransactionBound is the longest time that a transaction which
// records a step of a durable operation can stay open. SQLite allows one
// writer at a time, so a step transaction that stalls blocks every writer of
// the database. At the bound the transaction rolls back and releases the
// write lock. The bound is below the busy timeout of the store (10 s), so a
// writer that waits for a stalled step gets the lock before its own wait
// ends.
const DefaultStepTransactionBound = 5 * time.Second

// durableIDPrefix prefixes every operation ID that the store mints.
const durableIDPrefix = "op-"

// errUnknownDurableState reports an operation row whose state the store does
// not know.
var errUnknownDurableState = errors.New("durable operation row has an unknown state")

// durableColumns selects every column of durable_operations, in the order of
// db.DurableOperation.
const durableColumns = `SELECT id, start_key, kind, input, version, state, attempt, expiries, failures, ` +
	`last_error, claim_owner, claim_token, claim_expires_at_ms, cancel_requested, not_before_ms, created_at_ms, ` +
	`updated_at_ms`

// ErrStepTransactionBound reports a step transaction that stayed open past
// the bound of the store and rolled back. The write of the step changed
// nothing.
var ErrStepTransactionBound = errors.New("step transaction passed its time bound and rolled back")

// WithStepTransactionBound sets the bound of a step transaction. A value
// that is not positive keeps DefaultStepTransactionBound.
func WithStepTransactionBound(bound time.Duration) Option {
	return func(s *Store) {
		if bound > 0 {
			s.stepTxBound = bound
		}
	}
}

// Start implements durable.Store.
func (o *DurableOperations) Start(ctx context.Context, req durable.StartRequest) (durable.Operation, bool, error) {
	s := o.store

	if err := req.Validate(); err != nil {
		return durable.Operation{}, false, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var (
		op      durable.Operation
		created bool
	)

	err := s.inControlTx(ctx, func(q *db.Queries) error {
		var startErr error

		op, created, startErr = startDurable(ctx, q, req)

		return startErr
	})
	if err != nil {
		return durable.Operation{}, false, err
	}

	return op, created, nil
}

// Get implements durable.Store.
func (o *DurableOperations) Get(ctx context.Context, id durable.ID) (durable.Operation, error) {
	s := o.store

	s.mu.RLock()
	defer s.mu.RUnlock()

	var op durable.Operation

	err := s.inReadTx(ctx, func(q *db.Queries) error {
		row, err := q.GetDurableOperation(ctx, string(id))
		if errors.Is(err, sql.ErrNoRows) {
			return durable.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("read durable operation: %w", err)
		}

		op, err = loadDurable(ctx, q, row)

		return err
	})
	if err != nil {
		return durable.Operation{}, err
	}

	return op, nil
}

// List implements durable.Store.
func (o *DurableOperations) List(ctx context.Context, f durable.Filter) ([]durable.Operation, error) {
	s := o.store

	query, args := durableListQuery(f)

	s.mu.RLock()
	defer s.mu.RUnlock()

	tx, err := s.conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelDefault, ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin read transaction: %w", err)
	}

	out, err := listDurable(ctx, tx, query, args)
	if err != nil {
		return nil, errors.Join(err, rollback(tx))
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit read transaction: %w", err)
	}

	return out, nil
}

// listDurable runs the list query in tx and loads the steps of each
// operation in the same transaction, so the list is one snapshot.
func listDurable(ctx context.Context, tx *sql.Tx, query string, args []any) ([]durable.Operation, error) {
	found, err := queryDurableRows(ctx, tx, query, args)
	if err != nil {
		return nil, err
	}

	q := db.New(tx)
	out := make([]durable.Operation, 0, len(found))

	for i := range found {
		op, err := loadDurable(ctx, q, found[i])
		if err != nil {
			return nil, err
		}

		out = append(out, op)
	}

	return out, nil
}

// durableListQuery builds the list query of f: the kind and the states that
// f names, oldest first, with the limit of f.
func durableListQuery(f durable.Filter) (string, []any) {
	var (
		where []string
		args  []any
	)

	if f.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, string(f.Kind))
	}

	if len(f.States) > 0 {
		marks := make([]string, len(f.States))
		for i, state := range f.States {
			marks[i] = "?"
			args = append(args, string(state))
		}

		where = append(where, "state IN ("+strings.Join(marks, ", ")+")")
	}

	query := durableColumns + " FROM durable_operations"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	query += " ORDER BY created_at_ms, id"

	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}

	return query, args
}

// Claim implements durable.Store. It runs in one IMMEDIATE transaction, so
// of several concurrent claims of one operation only one sees it due.
func (o *DurableOperations) Claim(ctx context.Context, req durable.ClaimRequest) (durable.Operation, error) {
	s := o.store

	if err := req.Validate(); err != nil {
		return durable.Operation{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var (
		claimed durable.Operation
		found   bool
	)

	// The transaction commits also when no operation is due, so the
	// operations that claimNext ended canceled stay ended.
	err := s.inWriteTx(ctx, func(tx *sql.Tx, q *db.Queries) error {
		var claimErr error

		claimed, found, claimErr = claimNext(ctx, tx, q, req)

		return claimErr
	})
	if err != nil {
		return durable.Operation{}, err
	}

	if !found {
		return durable.Operation{}, durable.ErrNoWork
	}

	return claimed, nil
}

// claimNext claims the oldest operation that req allows and that can run
// now, in the transaction of tx, and reports false when no operation is due.
// It ends canceled each expired operation with a requested cancel that it
// finds first.
func claimNext(
	ctx context.Context,
	tx *sql.Tx,
	q *db.Queries,
	req durable.ClaimRequest,
) (durable.Operation, bool, error) {
	now, err := storeNow(ctx, q)
	if err != nil {
		return durable.Operation{}, false, err
	}

	query, args := durableClaimQuery(req, now)

	for {
		rows, err := queryDurableRows(ctx, tx, query, args)
		if err != nil {
			return durable.Operation{}, false, fmt.Errorf("find durable operation to claim: %w", err)
		}

		if len(rows) == 0 {
			var none durable.Operation

			return none, false, nil
		}

		row := rows[0]

		if row.State == string(durable.StateRunning) && row.CancelRequested {
			// The holder of a canceled operation stopped before its end:
			// the claim ends the operation canceled.
			if writeErr := writeDurable(ctx, q, endCanceled(row, now)); writeErr != nil {
				return durable.Operation{}, false, writeErr
			}

			continue
		}

		row = takeClaim(row, req, now)

		if writeErr := writeDurable(ctx, q, row); writeErr != nil {
			return durable.Operation{}, false, writeErr
		}

		op, loadErr := loadDurable(ctx, q, row)

		return op, true, loadErr
	}
}

// takeClaim gives row a new claim for req at now.
func takeClaim(row db.DurableOperation, req durable.ClaimRequest, now int64) db.DurableOperation {
	if row.State == string(durable.StateRunning) {
		// A running operation is due only when its claim expired: its
		// holder stopped without an end write.
		row.Expiries++
	}

	row.State = string(durable.StateRunning)
	row.Attempt++
	// The token grows on each claim and never goes back, also when a claim
	// ends, so a holder that lost its claim cannot write.
	row.ClaimToken++
	row.ClaimOwner = sql.NullString{String: req.Owner, Valid: true}
	row.ClaimExpiresAtMs = sql.NullInt64{Int64: now + durationMs(req.Lease), Valid: true}
	row.UpdatedAtMs = now

	return row
}

// durableClaimQuery builds the query for the oldest operation that req
// allows and that can run at now: a pending operation whose not_before_ms
// passed, or a running operation whose claim expired. The kinds of req and
// the version limit of each kind are bind parameters.
func durableClaimQuery(req durable.ClaimRequest, now int64) (string, []any) {
	kinds := make([]string, 0, len(req.Kinds))
	var args []any

	for _, kind := range req.Kinds {
		limit, ok := req.MaxVersions[kind]
		if !ok {
			kinds = append(kinds, "kind = ?")
			args = append(args, string(kind))

			continue
		}

		kinds = append(kinds, "(kind = ? AND version <= ?)")
		args = append(args, string(kind), limit)
	}

	args = append(args, now, now)

	query := durableColumns + " FROM durable_operations" +
		" WHERE (" + strings.Join(kinds, " OR ") + ")" +
		" AND state IN ('pending', 'running')" +
		" AND ((state = 'pending' AND not_before_ms <= ?)" +
		" OR (state = 'running' AND claim_expires_at_ms <= ?))" +
		" ORDER BY created_at_ms, id LIMIT 1"

	return query, args
}

// Extend implements durable.Store.
func (o *DurableOperations) Extend(ctx context.Context, c durable.Claim, lease time.Duration) (durable.Claim, error) {
	s := o.store

	if lease <= 0 {
		return durable.Claim{}, durable.ErrInvalid
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var extended durable.Claim

	err := s.inControlTx(ctx, func(q *db.Queries) error {
		row, now, err := liveDurable(ctx, q, c.Operation, c.Token)
		if err != nil {
			return err
		}

		if row.CancelRequested {
			return durable.ErrCanceled
		}

		row.ClaimExpiresAtMs = sql.NullInt64{Int64: now + durationMs(lease), Valid: true}
		row.UpdatedAtMs = now

		if err := writeDurable(ctx, q, row); err != nil {
			return err
		}

		extended = claimOf(row)

		return nil
	})
	if err != nil {
		return durable.Claim{}, err
	}

	return extended, nil
}

// RecordStep implements durable.Store. The transaction has the bound of a
// step transaction.
func (o *DurableOperations) RecordStep(ctx context.Context, f durable.StepFence, result []byte) error {
	s := o.store

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inStepTx(ctx, func(ctx context.Context, _ *sql.Tx, q *db.Queries) error {
		return recordStep(ctx, q, f, result)
	})
}

// Complete implements durable.Store.
func (o *DurableOperations) Complete(ctx context.Context, c durable.Claim) error {
	return o.store.endAttempt(ctx, c, completeEnd)
}

// Fail implements durable.Store.
func (o *DurableOperations) Fail(ctx context.Context, c durable.Claim, f durable.Failure) error {
	return o.store.endAttempt(ctx, c, failEnd(f))
}

// Release implements durable.Store.
func (o *DurableOperations) Release(ctx context.Context, c durable.Claim) error {
	return o.store.endAttempt(ctx, c, releaseEnd)
}

// endRule returns the row of the end of a claim at now, or the error that
// stops the end.
type endRule func(row db.DurableOperation, now int64) (db.DurableOperation, error)

// completeEnd is the rule of durable.Store.Complete.
func completeEnd(row db.DurableOperation, now int64) (db.DurableOperation, error) {
	if row.CancelRequested {
		return row, durable.ErrCanceled
	}

	row.State = string(durable.StateSucceeded)

	return clearClaim(row, now), nil
}

// failEnd returns the rule of durable.Store.Fail with f.
func failEnd(f durable.Failure) endRule {
	return func(row db.DurableOperation, now int64) (db.DurableOperation, error) {
		if row.CancelRequested {
			return endCanceled(row, now), nil
		}

		row.Failures++
		row.LastError = f.Message

		if f.Final {
			row.State = string(durable.StateFailed)
		} else {
			row.State = string(durable.StatePending)
			row.NotBeforeMs = now + durationMs(f.RetryAfter)
		}

		return clearClaim(row, now), nil
	}
}

// releaseEnd is the rule of durable.Store.Release.
func releaseEnd(row db.DurableOperation, now int64) (db.DurableOperation, error) {
	if row.CancelRequested {
		return endCanceled(row, now), nil
	}

	row.State = string(durable.StatePending)
	row.NotBeforeMs = now

	return clearClaim(row, now), nil
}

// Cancel implements durable.Store.
func (o *DurableOperations) Cancel(ctx context.Context, id durable.ID) error {
	s := o.store

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inControlTx(ctx, func(q *db.Queries) error {
		return cancelDurable(ctx, q, id)
	})
}

// cancelDurable applies the rules of durable.Store.Cancel to the operation id
// in the transaction of q. A domain write that cancels an operation in its
// own transaction calls it.
func cancelDurable(ctx context.Context, q *db.Queries, id durable.ID) error {
	row, err := q.GetDurableOperation(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return durable.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("read durable operation: %w", err)
	}

	now, err := storeNow(ctx, q)
	if err != nil {
		return err
	}

	switch durable.State(row.State) {
	case durable.StatePending:
		row.State = string(durable.StateCanceled)
	case durable.StateRunning:
		row.CancelRequested = true
	case durable.StateSucceeded, durable.StateFailed:
		return durable.ErrFinished
	case durable.StateCanceled:
		return nil
	default:
		return fmt.Errorf(
			"durable operation %s has the unknown state %q: %w",
			id,
			row.State,
			errUnknownDurableState,
		)
	}

	row.UpdatedAtMs = now

	return writeDurable(ctx, q, row)
}

// endAttempt runs the checks that Complete, Fail and Release share, then
// writes the row that end returns. An error from end changes nothing.
func (s *Store) endAttempt(ctx context.Context, c durable.Claim, end endRule) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inControlTx(ctx, func(q *db.Queries) error {
		return endClaim(ctx, q, c, end)
	})
}

// endClaim checks that c is the live claim of its operation in the
// transaction of q, then writes the row that end returns. An error from end
// changes nothing. A domain write that ends an operation in its own
// transaction calls it.
func endClaim(ctx context.Context, q *db.Queries, c durable.Claim, end endRule) error {
	row, now, err := liveDurable(ctx, q, c.Operation, c.Token)
	if err != nil {
		return err
	}

	ended, err := end(row, now)
	if err != nil {
		return err
	}

	return writeDurable(ctx, q, ended)
}

// inStepTx runs fn in an IMMEDIATE transaction with the bound of a step
// transaction, and gives fn the bounded context. At the bound, database/sql
// rolls the transaction back and releases the write lock, a statement of fn
// that runs is canceled, and the later statements of fn fail. A store method
// of another port that takes a durable.StepFence runs its write and
// recordStep through it, with the context that it gets.
func (s *Store) inStepTx(
	ctx context.Context, fn func(ctx context.Context, tx *sql.Tx, q *db.Queries) error,
) error {
	bounded, cancel := context.WithTimeout(ctx, s.stepTxBound)
	defer cancel()

	err := s.inWriteTx(bounded, func(tx *sql.Tx, q *db.Queries) error {
		return fn(bounded, tx, q)
	})

	return stepTxError(bounded, err)
}

// inWriteTx runs fn in an IMMEDIATE transaction (the _txlock of the store),
// with the transaction for the hand-written queries of fn.
func (s *Store) inWriteTx(ctx context.Context, fn func(tx *sql.Tx, q *db.Queries) error) error {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(tx, s.queries.WithTx(tx)); err != nil {
		return errors.Join(err, rollback(tx))
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// stepTxError adds ErrStepTransactionBound to err when the bound of the step
// transaction passed, so the caller can tell a stall from another failure.
func stepTxError(bounded context.Context, err error) error {
	if err != nil && errors.Is(bounded.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrStepTransactionBound, err)
	}

	return err
}

// startDurable starts the operation of req in the transaction of q, with the
// rules of durable.Store.Start. A domain write that starts an operation in
// its own transaction calls it.
func startDurable(ctx context.Context, q *db.Queries, req durable.StartRequest) (durable.Operation, bool, error) {
	if err := req.Validate(); err != nil {
		return durable.Operation{}, false, err
	}

	row, err := q.GetDurableOperationByKey(ctx, req.Key)

	switch {
	case err == nil:
		if row.Kind != string(req.Kind) {
			return durable.Operation{}, false, durable.ErrKeyConflict
		}

		op, loadErr := loadDurable(ctx, q, row)

		return op, false, loadErr
	case !errors.Is(err, sql.ErrNoRows):
		return durable.Operation{}, false, fmt.Errorf("read durable operation by key: %w", err)
	}

	now, err := storeNow(ctx, q)
	if err != nil {
		return durable.Operation{}, false, err
	}

	input := req.Input
	if input == nil {
		input = []byte{}
	}

	id := ident.NewID(durableIDPrefix)

	err = q.InsertDurableOperation(ctx, db.InsertDurableOperationParams{
		ID:          id,
		StartKey:    req.Key,
		Kind:        string(req.Kind),
		Input:       input,
		Version:     int64(req.Version),
		NotBeforeMs: now,
		CreatedAtMs: now,
		UpdatedAtMs: now,
	})
	if err != nil {
		return durable.Operation{}, false, fmt.Errorf("insert durable operation: %w", err)
	}

	row, err = q.GetDurableOperation(ctx, id)
	if err != nil {
		return durable.Operation{}, false, fmt.Errorf("read started durable operation: %w", err)
	}

	op, err := loadDurable(ctx, q, row)

	return op, true, err
}

// recordStep checks f and records its step with result in the transaction
// of q, with the rules of durable.Store.RecordStep in their order: the
// claim, the cancel, the step. It reads the clock of the store when it
// checks, not when the transaction began, so a claim that expired during a
// long transaction cannot write. A domain write that carries a StepFence
// calls it in its own transaction.
func recordStep(ctx context.Context, q *db.Queries, f durable.StepFence, result []byte) error {
	row, now, err := liveDurable(ctx, q, f.Operation, f.Token)
	if err != nil {
		return err
	}

	if row.CancelRequested {
		return durable.ErrCanceled
	}

	done, err := q.CountDurableStep(ctx, db.CountDurableStepParams{OperationID: row.ID, Name: f.Step})
	if err != nil {
		return fmt.Errorf("read durable step: %w", err)
	}

	if done > 0 {
		return durable.ErrStepDone
	}

	if result == nil {
		// The column is NOT NULL in both dialects; an empty value stands
		// for a step without a result.
		result = []byte{}
	}

	err = q.InsertDurableStep(ctx, db.InsertDurableStepParams{
		OperationID: row.ID,
		Name:        f.Step,
		Result:      result,
		DoneAtMs:    now,
	})
	if err != nil {
		return fmt.Errorf("insert durable step: %w", err)
	}

	row.UpdatedAtMs = now

	return writeDurable(ctx, q, row)
}

// liveDurable reads operation id and the clock of the store, and returns
// durable.ErrClaimLost unless token is the live claim of a running
// operation at that time.
func liveDurable(ctx context.Context, q *db.Queries, id durable.ID, token int64) (db.DurableOperation, int64, error) {
	row, err := q.GetDurableOperation(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return db.DurableOperation{}, 0, durable.ErrClaimLost
	}

	if err != nil {
		return db.DurableOperation{}, 0, fmt.Errorf("read durable operation: %w", err)
	}

	now, err := storeNow(ctx, q)
	if err != nil {
		return db.DurableOperation{}, 0, err
	}

	expired := !row.ClaimExpiresAtMs.Valid || now >= row.ClaimExpiresAtMs.Int64
	if row.State != string(durable.StateRunning) || expired || row.ClaimToken != token {
		return db.DurableOperation{}, 0, durable.ErrClaimLost
	}

	return row, now, nil
}

// storeNow returns the time of the clock of the store in Unix milliseconds,
// from the one SQL expression of DatabaseNowMs. SQLite has no server: its
// "now" is the clock of the host, which every handle on the database file
// shares. No time comes from a clock that the caller injects, so the clock
// offset of an instance changes no decision.
func storeNow(ctx context.Context, q *db.Queries) (int64, error) {
	now, err := q.DatabaseNowMs(ctx)
	if err != nil {
		return 0, fmt.Errorf("read the clock of the store: %w", err)
	}

	return now, nil
}

// writeDurable writes the columns of row that change after the start.
func writeDurable(ctx context.Context, q *db.Queries, row db.DurableOperation) error {
	err := q.UpdateDurableOperation(ctx, db.UpdateDurableOperationParams{
		State:            row.State,
		Attempt:          row.Attempt,
		Expiries:         row.Expiries,
		Failures:         row.Failures,
		LastError:        row.LastError,
		ClaimOwner:       row.ClaimOwner,
		ClaimToken:       row.ClaimToken,
		ClaimExpiresAtMs: row.ClaimExpiresAtMs,
		CancelRequested:  row.CancelRequested,
		NotBeforeMs:      row.NotBeforeMs,
		UpdatedAtMs:      row.UpdatedAtMs,
		ID:               row.ID,
	})
	if err != nil {
		return fmt.Errorf("write durable operation: %w", err)
	}

	return nil
}

// clearClaim removes the claim of row at now. The token stays, so the next
// claim gets a higher one.
func clearClaim(row db.DurableOperation, now int64) db.DurableOperation {
	row.ClaimOwner = sql.NullString{String: "", Valid: false}
	row.ClaimExpiresAtMs = sql.NullInt64{Int64: 0, Valid: false}
	row.UpdatedAtMs = now

	return row
}

// endCanceled ends row canceled at now.
func endCanceled(row db.DurableOperation, now int64) db.DurableOperation {
	row.State = string(durable.StateCanceled)

	return clearClaim(row, now)
}

// claimOf returns the claim of row, or the zero claim when row has none.
func claimOf(row db.DurableOperation) durable.Claim {
	if !row.ClaimOwner.Valid || !row.ClaimExpiresAtMs.Valid {
		return durable.Claim{Operation: "", Owner: "", Token: 0, ExpiresAt: time.Time{}}
	}

	return durable.Claim{
		Operation: durable.ID(row.ID),
		Owner:     row.ClaimOwner.String,
		Token:     row.ClaimToken,
		ExpiresAt: time.UnixMilli(row.ClaimExpiresAtMs.Int64).UTC(),
	}
}

// loadDurable maps row and its steps to an operation.
func loadDurable(ctx context.Context, q *db.Queries, row db.DurableOperation) (durable.Operation, error) {
	steps, err := q.ListDurableSteps(ctx, row.ID)
	if err != nil {
		return durable.Operation{}, fmt.Errorf("read durable steps: %w", err)
	}

	var records []durable.StepRecord

	for _, step := range steps {
		result := step.Result
		if len(result) == 0 {
			result = nil
		}

		records = append(records, durable.StepRecord{
			Name:   step.Name,
			Result: result,
			DoneAt: time.UnixMilli(step.DoneAtMs).UTC(),
		})
	}

	input := row.Input
	if len(input) == 0 {
		input = nil
	}

	return durable.Operation{
		ID:              durable.ID(row.ID),
		Key:             row.StartKey,
		Kind:            durable.Kind(row.Kind),
		Version:         int(row.Version),
		Input:           input,
		State:           durable.State(row.State),
		Steps:           records,
		Attempt:         int(row.Attempt),
		Failures:        int(row.Failures),
		Expiries:        int(row.Expiries),
		LastError:       row.LastError,
		Claim:           claimOf(row),
		CancelRequested: row.CancelRequested,
		NotBefore:       time.UnixMilli(row.NotBeforeMs).UTC(),
		CreatedAt:       time.UnixMilli(row.CreatedAtMs).UTC(),
		UpdatedAt:       time.UnixMilli(row.UpdatedAtMs).UTC(),
	}, nil
}

// queryDurableRows runs query in tx and scans the operation rows.
func queryDurableRows(ctx context.Context, tx *sql.Tx, query string, args []any) ([]db.DurableOperation, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list durable operations: %w", err)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // rows.Err below reports a read failure; a close error adds nothing.
	}()

	var found []db.DurableOperation

	for rows.Next() {
		var row db.DurableOperation

		err := rows.Scan(
			&row.ID, &row.StartKey, &row.Kind, &row.Input, &row.Version, &row.State, &row.Attempt, &row.Expiries,
			&row.Failures, &row.LastError, &row.ClaimOwner, &row.ClaimToken, &row.ClaimExpiresAtMs,
			&row.CancelRequested, &row.NotBeforeMs, &row.CreatedAtMs, &row.UpdatedAtMs,
		)
		if err != nil {
			return nil, fmt.Errorf("read durable operation: %w", err)
		}

		found = append(found, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list durable operations: %w", err)
	}

	return found, nil
}

// durationMs returns d in whole milliseconds, rounded up, so a positive
// duration below one millisecond is not 0.
func durationMs(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}

	return int64((d + time.Millisecond - 1) / time.Millisecond)
}
