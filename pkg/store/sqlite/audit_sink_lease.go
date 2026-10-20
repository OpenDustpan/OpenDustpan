package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ audit.SinkLeaser = (*Store)(nil)

// sinkLeaseTTL checks the arguments of a sink lease take and answers ttl
// in milliseconds.
func sinkLeaseTTL(sink, holder string, ttl time.Duration) (int64, error) {
	if sink == "" || holder == "" || ttl <= 0 {
		return 0, fmt.Errorf("%w: sink %q holder %q ttl %s", audit.ErrInvalidSinkLease, sink, holder, ttl)
	}

	ttlMs := ttl.Milliseconds()
	if ttlMs <= 0 {
		return 0, fmt.Errorf("%w: ttl %s is below one millisecond", audit.ErrInvalidSinkLease, ttl)
	}

	return ttlMs, nil
}

// ensureSinkOperation starts the durable operation that holds the lease of
// sink when it does not exist yet.
func ensureSinkOperation(ctx context.Context, q *db.Queries, sink string) error {
	_, _, err := startDurable(ctx, q, durable.StartRequest{
		Key:     audit.SinkLeaseKey(sink),
		Kind:    audit.SinkLeaseKind,
		Version: 0,
		Input:   nil,
	})
	if err != nil {
		return fmt.Errorf("start sink lease %q: %w", sink, err)
	}

	return nil
}

// takeSinkClaim answers the sink operation row after the claim decision for
// holder at now: a renewed claim when holder already holds a live claim, a
// new claim with a higher token otherwise. It answers false, with the row
// unchanged, while another instance holds a live claim.
func takeSinkClaim(row db.DurableOperation, holder string, ttlMs, now int64) (db.DurableOperation, bool) {
	live := row.State == string(durable.StateRunning) && row.ClaimExpiresAtMs.Valid &&
		now < row.ClaimExpiresAtMs.Int64

	switch {
	case live && row.ClaimOwner.String != holder:
		return row, false
	case live:
		row.ClaimExpiresAtMs = sql.NullInt64{Int64: now + ttlMs, Valid: true}
		row.UpdatedAtMs = now

		return row, true
	default:
		if row.State == string(durable.StateRunning) {
			row.Expiries++
		}

		row.State = string(durable.StateRunning)
		row.Attempt++
		row.ClaimToken++
		row.ClaimOwner = sql.NullString{String: holder, Valid: true}
		row.ClaimExpiresAtMs = sql.NullInt64{Int64: now + ttlMs, Valid: true}
		row.UpdatedAtMs = now

		return row, true
	}
}

// AcquireSinkLease implements audit.SinkLeaser on the durable operation of
// the sink, with the claim rule of the durable port. It starts the
// operation when it does not exist yet, then takes, renews or refuses the
// claim in one transaction on the clock of the store: a new claim with a
// higher token when no claim is live, the same claim with a later expiry
// when the caller already holds it, and durable.ErrNoWork, with no change,
// while another instance holds a live claim.
func (s *Store) AcquireSinkLease(ctx context.Context, sink, holder string, ttl time.Duration) (durable.Claim, error) {
	ttlMs, err := sinkLeaseTTL(sink, holder, ttl)
	if err != nil {
		return durable.Claim{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var claim durable.Claim

	err = s.inControlTx(ctx, func(q *db.Queries) error {
		if ensureErr := ensureSinkOperation(ctx, q, sink); ensureErr != nil {
			return ensureErr
		}

		row, readErr := q.GetDurableOperationByKey(ctx, audit.SinkLeaseKey(sink))
		if readErr != nil {
			return fmt.Errorf("read sink lease %q: %w", sink, readErr)
		}

		now, nowErr := storeNow(ctx, q)
		if nowErr != nil {
			return nowErr
		}

		claimed, held := takeSinkClaim(row, holder, ttlMs, now)
		if !held {
			return durable.ErrNoWork
		}

		if writeErr := writeDurable(ctx, q, claimed); writeErr != nil {
			return writeErr
		}

		claim = claimOf(claimed)

		return nil
	})
	if err != nil {
		return durable.Claim{}, err
	}

	return claim, nil
}

// ReleaseSinkLease implements audit.SinkLeaser. It clears the claim of the
// sink operation of claim, and returns the operation to pending, in one
// transaction. It answers durable.ErrClaimLost, and changes nothing, when
// claim is not the live claim of its operation.
func (s *Store) ReleaseSinkLease(ctx context.Context, claim durable.Claim) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inControlTx(ctx, func(q *db.Queries) error {
		row, err := q.GetDurableOperation(ctx, string(claim.Operation))
		if errors.Is(err, sql.ErrNoRows) {
			return durable.ErrClaimLost
		}

		if err != nil {
			return fmt.Errorf("read sink lease: %w", err)
		}

		if row.Kind != string(audit.SinkLeaseKind) {
			return fmt.Errorf("%w: operation %q is not a sink lease", audit.ErrInvalidSinkLease, claim.Operation)
		}

		live, now, err := liveDurable(ctx, q, claim.Operation, claim.Token)
		if err != nil {
			return err
		}

		live.State = string(durable.StatePending)
		live.NotBeforeMs = now

		return writeDurable(ctx, q, clearClaim(live, now))
	})
}

// checkSinkClaim answers the time of the check on the clock of the store,
// or durable.ErrClaimLost unless claim is the live claim of sink: the
// operation of sink is running under the token of claim and its expiry has
// not passed. A cursor write calls it in the transaction of the write, so
// a holder that lost its claim cannot advance a cursor.
func checkSinkClaim(ctx context.Context, q *db.Queries, sink string, claim durable.Claim) (int64, error) {
	row, err := q.GetDurableOperationByKey(ctx, audit.SinkLeaseKey(sink))
	if errors.Is(err, sql.ErrNoRows) {
		return 0, durable.ErrClaimLost
	}

	if err != nil {
		return 0, fmt.Errorf("read sink lease %q: %w", sink, err)
	}

	if row.ID != string(claim.Operation) || row.ClaimToken != claim.Token {
		return 0, durable.ErrClaimLost
	}

	now, err := storeNow(ctx, q)
	if err != nil {
		return 0, err
	}

	expired := !row.ClaimExpiresAtMs.Valid || now >= row.ClaimExpiresAtMs.Int64
	if row.State != string(durable.StateRunning) || expired {
		return 0, durable.ErrClaimLost
	}

	return now, nil
}
