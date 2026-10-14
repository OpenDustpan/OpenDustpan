package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ audit.ChainSwitch = (*Store)(nil)

// SetChain implements audit.ChainSwitch for later appends of this handle.
func (s *Store) SetChain(on bool) {
	s.chain.Set(on)
}

// HasChainedEntries implements audit.ChainSwitch.
func (s *Store) HasChainedEntries(ctx context.Context) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	found, err := s.queries.HasChainedAuditEntries(ctx)
	if err != nil {
		return false, fmt.Errorf("read chained audit entries: %w", err)
	}

	return found, nil
}

// ChainRecord implements audit.ChainSwitch. A missing row reads as no
// record.
func (s *Store) ChainRecord(ctx context.Context) (audit.ChainRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetAuditChainState(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return audit.ChainRecord{State: audit.ChainUnknown, EverChained: false}, nil
	}

	if err != nil {
		return audit.ChainRecord{}, fmt.Errorf("read audit chain state: %w", err)
	}

	return audit.ChainRecord{State: audit.ChainState(row.State), EverChained: row.EverChained != 0}, nil
}

// RecordChain implements audit.ChainSwitch. The write lock of the
// transaction orders the appends of the server scope.
func (s *Store) RecordChain(ctx context.Context, rec audit.ChainRecord, entries []audit.ChainEntry) error {
	for i := range entries {
		if err := validServerEntry(entries[i].Entry); err != nil {
			return err
		}
	}

	var ever int64
	if rec.EverChained {
		ever = 1
	}

	err := s.inTx(ctx, func(q *db.Queries) error {
		if err := s.insertChainEntries(ctx, q, entries); err != nil {
			return err
		}

		params := db.SetAuditChainStateParams{State: string(rec.State), EverChained: ever}
		if err := q.SetAuditChainState(ctx, params); err != nil {
			return fmt.Errorf("save audit chain state: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	s.chain.Set(rec.State == audit.ChainStateOn)

	return nil
}

// insertChainEntries appends the switch entries in q, each with a hash only
// when it is Chained.
func (s *Store) insertChainEntries(ctx context.Context, q *db.Queries, entries []audit.ChainEntry) error {
	for i := range entries {
		chain := audit.ChainStateOff
		if entries[i].Chained {
			chain = audit.ChainStateOn
		}

		if _, err := s.insertAuditEntry(ctx, q, entries[i].Entry, chain); err != nil {
			return fmt.Errorf("append audit chain entry: %w", err)
		}
	}

	return nil
}

// readChainState reads the recorded chain state in q, and false when no
// row exists.
func readChainState(ctx context.Context, q *db.Queries) (audit.ChainState, bool, error) {
	row, err := q.GetAuditChainState(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return audit.ChainUnknown, false, nil
	}

	if err != nil {
		return audit.ChainUnknown, false, fmt.Errorf("read audit chain state: %w", err)
	}

	return audit.ChainState(row.State), true, nil
}

// chainPoints answers the timeline of the chain from the switch entries of
// the server scope, for the chain check.
func chainPoints(ctx context.Context, q *db.Queries) ([]audit.ChainPoint, error) {
	rows, err := q.ListAuditChainSwitches(ctx)
	if err != nil {
		return nil, fmt.Errorf("select audit chain switches: %w", err)
	}

	out := make([]audit.ChainPoint, 0, len(rows))

	for _, row := range rows {
		if p, ok := audit.ChainPointFromPath(time.UnixMilli(row.AtMs), row.ValuePath); ok {
			out = append(out, p)
		}
	}

	return out, nil
}
