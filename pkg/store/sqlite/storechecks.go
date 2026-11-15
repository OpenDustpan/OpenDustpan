package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/storecheck"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ storecheck.Records = (*Store)(nil)

// StartCheck implements storecheck.Records: it stores r as a running check and
// starts the operation start in one transaction. The unique index on running
// checks allows one running check. A check on the same target that ended after
// coolingSince refuses the start. A failed start changes nothing.
func (s *Store) StartCheck(
	ctx context.Context, r storecheck.Record, start durable.StartRequest, coolingSince time.Time,
) error {
	// Defense in depth: the service validates the row and builds the request
	// from the ID. These checks stay so that no caller can store a row that
	// the table checks refuse, or start the row with the operation of another
	// kind or key.
	if err := r.ValidateStart(); err != nil {
		return fmt.Errorf("start store check: %w", err)
	}

	if err := storecheck.ValidateOperation(r.ID, start); err != nil {
		return fmt.Errorf("start store check: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inControlTx(ctx, func(q *db.Queries) error {
		n, err := q.CreateStoreCheck(ctx, db.CreateStoreCheckParams{
			ID:          r.ID.String(),
			Target:      string(r.Target),
			RequestedBy: r.RequestedBy,
			StartedAtMs: codec.Millis(r.StartedAt),
		})
		if err != nil {
			return fmt.Errorf("insert store check: %w", err)
		}

		if n == 0 {
			return storeCheckConflict(ctx, q, r.ID)
		}

		ended, err := q.CountStoreChecksEndedSince(ctx, db.CountStoreChecksEndedSinceParams{
			Target: string(r.Target), EndedAtMs: nullMillis(codec.Millis(coolingSince)),
		})
		if err != nil {
			return fmt.Errorf("count ended store checks: %w", err)
		}

		if ended > 0 {
			return storecheck.ErrCooldown
		}

		_, created, err := startDurable(ctx, q, start)
		if err != nil {
			return err
		}

		if !created {
			return fmt.Errorf("start store check operation %q: %w", start.Key, durable.ErrKeyConflict)
		}

		return nil
	})
}

// storeCheckConflict tells why the insert of a row changed nothing: the ID is
// taken, or the unique index refused a second running check.
func storeCheckConflict(ctx context.Context, q *db.Queries, id storecheck.ID) error {
	_, err := q.GetStoreCheck(ctx, id.String())
	if err == nil {
		return storecheck.ErrExists
	}

	if errors.Is(err, sql.ErrNoRows) {
		return storecheck.ErrRunning
	}

	return fmt.Errorf("select store check: %w", err)
}

// GetCheck implements storecheck.Records.
func (s *Store) GetCheck(ctx context.Context, id storecheck.ID) (storecheck.Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetStoreCheck(ctx, id.String())
	if errors.Is(err, sql.ErrNoRows) {
		return storecheck.Record{}, storecheck.ErrNotFound
	}

	if err != nil {
		return storecheck.Record{}, fmt.Errorf("select store check: %w", err)
	}

	return storeCheckRecord(row), nil
}

// ListChecks implements storecheck.Records: every row, newest first.
func (s *Store) ListChecks(ctx context.Context) ([]storecheck.Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.ListStoreChecks(ctx)
	if err != nil {
		return nil, fmt.Errorf("select store checks: %w", err)
	}

	out := make([]storecheck.Record, 0, len(rows))
	for _, row := range rows {
		out = append(out, storeCheckRecord(row))
	}

	return out, nil
}

// FinishCheck implements storecheck.Records: it checks the fence f, records
// its step and moves the running check id to its end state, in one
// transaction.
func (s *Store) FinishCheck(ctx context.Context, f durable.StepFence, id storecheck.ID, end storecheck.Finish) error {
	if err := end.Validate(); err != nil {
		return fmt.Errorf("finish store check: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inStepTx(ctx, func(ctx context.Context, _ *sql.Tx, q *db.Queries) error {
		// The fence comes first: a stale claim changes nothing and learns
		// nothing about the row.
		if err := recordStep(ctx, q, f, nil); err != nil {
			return err
		}

		// Defense in depth: the fence checked above is live, but it must
		// belong to the operation of this check. The service passes the right
		// fence; this check stays so that no caller can finish a row under the
		// claim of another operation.
		op, err := q.GetDurableOperation(ctx, string(f.Operation))
		if err != nil {
			return fmt.Errorf("select operation of the fence: %w", err)
		}

		if op.Kind != string(storecheck.OperationKind) || op.StartKey != storecheck.OperationKey(id) {
			return durable.ErrClaimLost
		}

		n, err := q.FinishStoreCheck(ctx, db.FinishStoreCheckParams{
			State: string(end.State), Error: end.Error, Report: string(end.Report),
			EndedAtMs: optionalMillis(end.EndedAt), ID: id.String(),
		})
		if err != nil {
			return fmt.Errorf("finish store check: %w", err)
		}

		if n == 0 {
			return finishStoreCheckFailure(ctx, q, id)
		}

		return nil
	})
}

// finishStoreCheckFailure tells why FinishCheck changed nothing.
func finishStoreCheckFailure(ctx context.Context, q *db.Queries, id storecheck.ID) error {
	if _, err := q.GetStoreCheck(ctx, id.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storecheck.ErrNotFound
		}

		return fmt.Errorf("select store check: %w", err)
	}

	return storecheck.ErrNotRunning
}

// FailEndedChecks implements storecheck.Records.
func (s *Store) FailEndedChecks(ctx context.Context, endedAt time.Time) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n, err := s.queries.FailEndedStoreChecks(ctx, db.FailEndedStoreChecksParams{
		Error:     storecheck.EndedWithoutFinishText,
		EndedAtMs: optionalMillis(endedAt),
		Prefix:    storecheck.OperationKeyPrefix,
		Kind:      string(storecheck.OperationKind),
	})
	if err != nil {
		return 0, fmt.Errorf("fail ended store checks: %w", err)
	}

	return int(n), nil
}

// storeCheckRecord converts a row into a domain record. A NULL end time
// reads as the zero time: the check still runs.
func storeCheckRecord(row db.StoreCheck) storecheck.Record {
	var endedAt time.Time
	if row.EndedAtMs.Valid {
		endedAt = codec.FromMillis(row.EndedAtMs.Int64)
	}

	var report []byte
	if row.Report != "" {
		report = []byte(row.Report)
	}

	return storecheck.Record{
		StartedAt:   codec.FromMillis(row.StartedAtMs),
		EndedAt:     endedAt,
		ID:          storecheck.ID(row.ID),
		Target:      storecheck.Target(row.Target),
		State:       storecheck.State(row.State),
		RequestedBy: row.RequestedBy,
		Error:       row.Error,
		Report:      report,
	}
}
