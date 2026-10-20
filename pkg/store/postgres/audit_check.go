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

// CheckState implements audit.CheckStore. A database without a result row
// answers a zero result, and a database without marks answers no marks.
func (s *Store) CheckState(ctx context.Context) (audit.CheckState, error) {
	var state audit.CheckState

	row, err := s.queries.GetAuditChainCheck(ctx)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No check ran yet: the result stays zero.
	case err != nil:
		return audit.CheckState{CheckedAt: time.Time{}, Marks: nil, Invalid: 0}, fmt.Errorf(
			"read audit chain check: %w",
			err,
		)
	default:
		state.CheckedAt = codec.FromOptionalMillis(row.CheckedAtMs)
		state.Invalid = int(row.Invalid)
	}

	marks, err := s.queries.ListAuditChainMarks(ctx)
	if err != nil {
		return audit.CheckState{CheckedAt: time.Time{}, Marks: nil, Invalid: 0}, fmt.Errorf(
			"read audit chain marks: %w",
			err,
		)
	}

	for _, m := range marks {
		state.Marks = append(state.Marks, audit.CheckMark{
			At:       codec.FromOptionalMillis(m.MarkedAtMs),
			Scope:    namespace.Name(m.Scope),
			Sequence: m.Sequence,
			Epoch:    m.Epoch,
		})
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
		err := q.SaveAuditChainMark(ctx, db.SaveAuditChainMarkParams{
			Epoch: mark.Epoch, Scope: mark.Scope.String(), Sequence: mark.Sequence,
		})
		if err != nil {
			return fmt.Errorf("save audit chain check mark: %w", err)
		}

		return nil
	})
}
