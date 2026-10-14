package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ audit.ChainSwitch = (*Store)(nil)

// SetChain implements audit.ChainSwitch for later appends of this handle.
func (s *Store) SetChain(on bool) {
	s.chainOff.Store(!on)
}

// HasChainedEntries implements audit.ChainSwitch.
func (s *Store) HasChainedEntries(ctx context.Context) (bool, error) {
	found, err := s.queries.HasChainedAuditEntries(ctx)
	if err != nil {
		return false, fmt.Errorf("read chained audit entries: %w", err)
	}

	return found, nil
}

// ChainRecord implements audit.ChainSwitch. A missing row reads as no
// record.
func (s *Store) ChainRecord(ctx context.Context) (audit.ChainRecord, error) {
	row, err := s.queries.GetAuditChainState(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return audit.ChainRecord{State: audit.ChainUnknown, EverChained: false}, nil
	}

	if err != nil {
		return audit.ChainRecord{}, fmt.Errorf("read audit chain state: %w", err)
	}

	return audit.ChainRecord{State: audit.ChainState(row.State), EverChained: row.EverChained}, nil
}

// SaveChainRecord implements audit.ChainSwitch.
func (s *Store) SaveChainRecord(ctx context.Context, rec audit.ChainRecord) error {
	err := s.queries.SetAuditChainState(ctx, db.SetAuditChainStateParams{
		State:       string(rec.State),
		EverChained: rec.EverChained,
	})
	if err != nil {
		return fmt.Errorf("save audit chain state: %w", err)
	}

	return nil
}

// chainOffTimes answers the times of the chained entries of the server
// scope that turned the chain off, for the chain check.
func chainOffTimes(ctx context.Context, q *db.Queries) ([]time.Time, error) {
	rows, err := q.ListAuditChainOffTimes(ctx)
	if err != nil {
		return nil, fmt.Errorf("select audit chain off times: %w", err)
	}

	out := make([]time.Time, 0, len(rows))
	for _, ms := range rows {
		out = append(out, time.UnixMilli(ms))
	}

	return out, nil
}
