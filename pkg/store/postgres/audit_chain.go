package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ audit.ChainSwitch = (*Store)(nil)

// SetChain implements audit.ChainSwitch for later appends of this handle.
func (s *Store) SetChain(on bool) {
	s.chain.Set(on)
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

// RecordChain implements audit.ChainSwitch. The advisory lock of the
// server scope orders its appends, as in AppendServer.
func (s *Store) RecordChain(ctx context.Context, rec audit.ChainRecord, entries []audit.ChainEntry) error {
	for i := range entries {
		if err := validServerEntry(entries[i].Entry); err != nil {
			return err
		}
	}

	err := s.inTx(ctx, func(q *db.Queries) error {
		if err := q.LockServerAuditScope(ctx); err != nil {
			return fmt.Errorf("lock server audit scope: %w", err)
		}

		if err := s.insertChainEntries(ctx, q, entries); err != nil {
			return err
		}

		params := db.SetAuditChainStateParams{State: string(rec.State), EverChained: rec.EverChained}
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
	if errors.Is(err, pgx.ErrNoRows) {
		return audit.ChainUnknown, false, nil
	}

	if err != nil {
		return audit.ChainUnknown, false, fmt.Errorf("read audit chain state: %w", err)
	}

	return audit.ChainState(row.State), true, nil
}

// chainTimeline answers the timeline of the chain for the chain check: the
// trusted switch entries of the server scope, and whether the installation
// holds a recorded chain state.
func chainTimeline(ctx context.Context, q *db.Queries) (audit.ChainTimeline, error) {
	_, recorded, err := readChainState(ctx, q)
	if err != nil {
		return audit.ChainTimeline{}, err
	}

	rows, err := q.ListAuditChainSwitches(ctx)
	if err != nil {
		return audit.ChainTimeline{}, fmt.Errorf("select audit chain switches: %w", err)
	}

	switches := make([]audit.Entry, 0, len(rows))

	for i := range rows {
		switches = append(switches, fromAuditRow(rows[i]))
	}

	return audit.ChainTimeline{Points: audit.TimelineFromSwitches(switches), Recorded: recorded}, nil
}
