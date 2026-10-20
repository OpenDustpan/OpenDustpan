package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ audit.CheckStore = (*Store)(nil)

// CheckState implements audit.CheckStore. A database without a row answers
// the zero state.
func (s *Store) CheckState(ctx context.Context) (audit.CheckState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetAuditChainCheck(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return audit.CheckState{CheckedAt: time.Time{}, Mark: nil, Invalid: 0}, nil
	}

	if err != nil {
		return audit.CheckState{
			CheckedAt: time.Time{},
			Mark:      nil,
			Invalid:   0,
		}, fmt.Errorf(
			"read audit chain check: %w",
			err,
		)
	}

	state := audit.CheckState{
		CheckedAt: codec.FromOptionalMillis(row.CheckedAtMs),
		Mark:      nil,
		Invalid:   int(row.Invalid),
	}

	if row.Marked != 0 {
		state.Mark = &audit.CheckMark{
			At:       codec.FromOptionalMillis(row.MarkedAtMs),
			Scope:    namespace.Name(row.MarkScope),
			Sequence: row.MarkSequence,
			Epoch:    row.MarkEpoch,
		}
	}

	return state, nil
}

// SaveCheckResult implements audit.CheckStore in one transaction. It touches
// no domain row, so the write can move onto the control path of the store
// without a change to the statement.
func (s *Store) SaveCheckResult(ctx context.Context, invalid int) error {
	if invalid < 0 {
		return fmt.Errorf("invalid count %d: %w", invalid, audit.ErrInvalidCheckState)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inTx(ctx, func(q *db.Queries) error {
		now, err := q.DatabaseNowMs(ctx)
		if err != nil {
			return fmt.Errorf("read the clock of the store: %w", err)
		}

		err = q.SaveAuditChainCheckResult(ctx, db.SaveAuditChainCheckResultParams{NowMs: now, Invalid: int64(invalid)})
		if err != nil {
			return fmt.Errorf("save audit chain check result: %w", err)
		}

		return nil
	})
}

// SaveCheckMark implements audit.CheckStore in one transaction, as
// SaveCheckResult.
func (s *Store) SaveCheckMark(ctx context.Context, mark audit.CheckMark) error {
	if err := mark.Validate(); err != nil {
		return fmt.Errorf("audit chain check mark: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inTx(ctx, func(q *db.Queries) error {
		now, err := q.DatabaseNowMs(ctx)
		if err != nil {
			return fmt.Errorf("read the clock of the store: %w", err)
		}

		err = q.SaveAuditChainCheckMark(ctx, db.SaveAuditChainCheckMarkParams{
			Epoch: mark.Epoch, Scope: mark.Scope.String(), Sequence: mark.Sequence, NowMs: now,
		})
		if err != nil {
			return fmt.Errorf("save audit chain check mark: %w", err)
		}

		return nil
	})
}
