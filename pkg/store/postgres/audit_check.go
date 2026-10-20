package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ audit.CheckStore = (*Store)(nil)

// CheckState implements audit.CheckStore. A database without a row answers
// the zero state.
func (s *Store) CheckState(ctx context.Context) (audit.CheckState, error) {
	row, err := s.queries.GetAuditChainCheck(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
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

	if row.Marked {
		state.Mark = &audit.CheckMark{
			At:       codec.FromOptionalMillis(row.MarkedAtMs),
			Scope:    namespace.Name(row.MarkScope),
			Sequence: row.MarkSequence,
			Epoch:    row.MarkEpoch,
		}
	}

	return state, nil
}

// SaveCheckResult implements audit.CheckStore in one transaction of one
// statement on the control path: the check stores its result in maintenance
// mode too.
func (s *Store) SaveCheckResult(ctx context.Context, invalid int) error {
	if invalid < 0 {
		return fmt.Errorf("invalid count %d: %w", invalid, audit.ErrInvalidCheckState)
	}

	return s.inControlTx(ctx, func(q *db.Queries) error {
		if err := q.SaveAuditChainCheckResult(ctx, int64(invalid)); err != nil {
			return fmt.Errorf("save audit chain check result: %w", err)
		}

		return nil
	})
}

// SaveCheckMark implements audit.CheckStore in one transaction of one
// statement, as SaveCheckResult.
func (s *Store) SaveCheckMark(ctx context.Context, mark audit.CheckMark) error {
	if err := mark.Validate(); err != nil {
		return fmt.Errorf("audit chain check mark: %w", err)
	}

	return s.inControlTx(ctx, func(q *db.Queries) error {
		err := q.SaveAuditChainCheckMark(ctx, db.SaveAuditChainCheckMarkParams{
			Epoch: mark.Epoch, Scope: mark.Scope.String(), Sequence: mark.Sequence,
		})
		if err != nil {
			return fmt.Errorf("save audit chain check mark: %w", err)
		}

		return nil
	})
}
