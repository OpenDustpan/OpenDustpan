package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ audit.SinkLeaser = (*Store)(nil)

// AcquireSinkLease implements audit.SinkLeaser on the durable operation of
// the sink, with the claim rule of the durable port. It starts the
// operation when it does not exist yet, then takes, renews or refuses the
// claim in one transaction on the clock of the store: a new claim with a
// higher token when no claim is live, the same claim with a later expiry
// when the caller already holds it, and durable.ErrNoWork, with no change,
// while another instance holds a live claim.
func (s *Store) AcquireSinkLease(
	ctx context.Context,
	sink, holder string,
	ttl time.Duration,
) (durable.Claim, error) {
	ttlMs, err := sinkLeaseTTL(sink, holder, ttl)
	if err != nil {
		return durable.Claim{}, err
	}

	var claim durable.Claim

	err = s.inTx(ctx, func(q *db.Queries) error {
		op, _, startErr := startOperation(ctx, q, durable.StartRequest{
			Key:     audit.SinkLeaseKey(sink),
			Kind:    audit.SinkLeaseKind,
			Version: 0,
			Input:   nil,
		})
		if startErr != nil {
			return fmt.Errorf("start sink lease %q: %w", sink, startErr)
		}

		row, lockErr := q.LockDurableOperation(ctx, string(op.ID))
		if lockErr != nil {
			return fmt.Errorf("lock sink lease %q: %w", sink, lockErr)
		}

		now, nowErr := q.DatabaseNowMs(ctx)
		if nowErr != nil {
			return fmt.Errorf("read the database clock: %w", nowErr)
		}

		claimed, claimErr := takeSinkClaim(ctx, q, sinkClaimBid{
			row: row, holder: holder, ttlMs: ttlMs, now: now, sink: sink,
		})
		if claimErr != nil {
			return claimErr
		}

		claim = claimed

		return nil
	})
	if err != nil {
		return durable.Claim{}, err
	}

	return claim, nil
}

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

// sinkClaimBid carries the locked sink operation row and the inputs of its
// claim decision.
type sinkClaimBid struct {
	row    db.DurableOperation
	holder string
	ttlMs  int64
	now    int64
	sink   string
}

// takeSinkClaim takes or renews the claim of the locked sink operation row
// for its holder at its time: a renew with the same token when the holder
// already holds a live claim, else a new claim with a higher token. It
// answers durable.ErrNoWork, with no change, while another instance holds
// a live claim.
func takeSinkClaim(ctx context.Context, q *db.Queries, bid sinkClaimBid) (durable.Claim, error) {
	row := bid.row

	live := row.State == string(durable.StateRunning) && row.ClaimExpiresAtMs.Valid &&
		bid.now < row.ClaimExpiresAtMs.Int64

	switch {
	case live && row.ClaimOwner.String != bid.holder:
		return durable.Claim{}, durable.ErrNoWork
	case live:
		return renewSinkClaim(ctx, q, bid)
	default:
		return claimSinkOperation(ctx, q, bid)
	}
}

// renewSinkClaim moves the expiry of the live claim of bid to its time
// plus its TTL, and answers the claim with the same token.
func renewSinkClaim(ctx context.Context, q *db.Queries, bid sinkClaimBid) (durable.Claim, error) {
	expires := bid.now + bid.ttlMs

	params := db.ExtendDurableClaimParams{ExpiresAtMs: expires, NowMs: bid.now, ID: bid.row.ID}
	if err := q.ExtendDurableClaim(ctx, params); err != nil {
		return durable.Claim{}, fmt.Errorf("renew sink lease %q: %w", bid.sink, err)
	}

	return durable.Claim{
		Operation: durable.ID(bid.row.ID), Owner: bid.holder, Token: bid.row.ClaimToken,
		ExpiresAt: codec.FromMillis(expires),
	}, nil
}

// claimSinkOperation claims the sink operation row of bid for its holder: a
// new claim with a higher token and an expiry at its time plus its TTL.
func claimSinkOperation(ctx context.Context, q *db.Queries, bid sinkClaimBid) (durable.Claim, error) {
	var add int64

	if bid.row.State == string(durable.StateRunning) {
		add = 1
	}

	claimed, err := q.ClaimSinkOperation(ctx, db.ClaimSinkOperationParams{
		ID: bid.row.ID, Owner: bid.holder, Token: bid.row.ClaimToken + 1, ExpiresAtMs: bid.now + bid.ttlMs,
		NowMs: bid.now, AddExpiries: add,
	})
	if err != nil {
		return durable.Claim{}, fmt.Errorf("claim sink lease %q: %w", bid.sink, err)
	}

	return durable.Claim{
		Operation: durable.ID(bid.row.ID), Owner: bid.holder, Token: claimed.ClaimToken,
		ExpiresAt: codec.FromMillis(claimed.ClaimExpiresAtMs.Int64),
	}, nil
}

// ReleaseSinkLease implements audit.SinkLeaser. It clears the claim of the
// sink operation of claim, and returns the operation to pending, in one
// transaction. It answers durable.ErrClaimLost, and changes nothing, when
// claim is not the live claim of its operation.
func (s *Store) ReleaseSinkLease(ctx context.Context, claim durable.Claim) error {
	err := s.inTx(ctx, func(q *db.Queries) error {
		row, err := q.GetDurableOperation(ctx, string(claim.Operation))
		if errors.Is(err, pgx.ErrNoRows) {
			return durable.ErrClaimLost
		}

		if err != nil {
			return fmt.Errorf("read sink lease: %w", err)
		}

		if row.Kind != string(audit.SinkLeaseKind) {
			return fmt.Errorf("%w: operation %q is not a sink lease", audit.ErrInvalidSinkLease, claim.Operation)
		}

		live, now, err := lockLive(ctx, q, claim.Operation, claim.Token)
		if err != nil {
			return err
		}

		params := endParams(live, durable.StatePending, live.LastError, now, now)

		if err := q.EndDurableClaim(ctx, params); err != nil {
			return fmt.Errorf("release sink lease: %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("release sink lease: %w", err)
	}

	return nil
}

// checkSinkClaim locks the operation of sink and answers the time of the
// check on the clock of the store, or durable.ErrClaimLost unless claim is
// its live claim. A cursor write calls it in the transaction of the write,
// so a holder that lost its claim cannot advance a cursor.
func checkSinkClaim(ctx context.Context, q *db.Queries, sink string, claim durable.Claim) (int64, error) {
	row, err := q.GetDurableOperationByKey(ctx, audit.SinkLeaseKey(sink))
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, durable.ErrClaimLost
	}

	if err != nil {
		return 0, fmt.Errorf("read sink lease %q: %w", sink, err)
	}

	_, now, err := lockLive(ctx, q, durable.ID(row.ID), claim.Token)
	if err != nil {
		return 0, err
	}

	return now, nil
}
