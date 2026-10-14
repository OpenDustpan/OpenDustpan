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

		// Two instances that start at the same time both write a start
		// entry. The record that the first one commits makes the second
		// skip it, so the timeline holds one start entry.
		current, err := readChainRecord(ctx, q)
		if err != nil {
			return err
		}

		if err := s.insertChainEntries(ctx, q, withoutStart(entries, current)); err != nil {
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

// readChainRecord reads the record of the chain in q. A missing row reads
// as no record.
func readChainRecord(ctx context.Context, q *db.Queries) (audit.ChainRecord, error) {
	row, err := q.GetAuditChainState(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return audit.ChainRecord{State: audit.ChainUnknown, EverChained: false}, nil
	}

	if err != nil {
		return audit.ChainRecord{}, fmt.Errorf("read audit chain state: %w", err)
	}

	return audit.ChainRecord{State: audit.ChainState(row.State), EverChained: row.EverChained}, nil
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

// chainTimeline answers the timeline of the chain for the chain check. It
// walks the server scope from sequence 1 up to its last switch entry in
// bounded pages, so only switch entries that the walk accepts in the chain
// count (see audit.TimelineBuilder). A row of the server scope below
// sequence 1 breaks the timeline.
func chainTimeline(ctx context.Context, q *db.Queries) (audit.ChainTimeline, error) {
	rec, err := readChainRecord(ctx, q)
	if err != nil {
		return audit.ChainTimeline{}, err
	}

	last, err := q.LastAuditChainSwitchSequence(ctx)
	if err != nil {
		return audit.ChainTimeline{}, fmt.Errorf("select last audit chain switch: %w", err)
	}

	lowest, err := q.MinAuditSequence(ctx, audit.ServerScope.String())
	if err != nil {
		return audit.ChainTimeline{}, fmt.Errorf("select lowest server audit sequence: %w", err)
	}

	b := audit.NewTimelineBuilder()
	if lowest < 1 {
		return audit.ChainTimeline{Points: nil, Recorded: rec.State != audit.ChainUnknown, Broken: true}, nil
	}

	if err := walkServerScope(ctx, q, b, last); err != nil {
		return audit.ChainTimeline{}, err
	}

	return b.Timeline(rec), nil
}

// walkServerScope adds the entries of the server scope from sequence 1 up
// to last to b, in pages of audit.CheckPageSize, and stops early when b
// breaks.
func walkServerScope(ctx context.Context, q *db.Queries, b *audit.TimelineBuilder, last int64) error {
	var after int64

	for after < last {
		rows, err := q.ListAuditChainPage(ctx, db.ListAuditChainPageParams{
			Namespace: audit.ServerScope.String(),
			Sequence:  after,
			Limit:     audit.CheckPageSize,
		})
		if err != nil {
			return fmt.Errorf("select server audit chain: %w", err)
		}

		for i := range rows {
			if rows[i].Sequence > last || !b.AddStored(fromAuditRow(rows[i]), rows[i].Changes.String) {
				return nil
			}

			after = rows[i].Sequence
		}

		if len(rows) < audit.CheckPageSize {
			return nil
		}
	}

	return nil
}

// withoutStart answers entries without the start entry when the
// installation already holds a record (see audit.ChainEntry.Start).
func withoutStart(entries []audit.ChainEntry, current audit.ChainRecord) []audit.ChainEntry {
	if current.State == audit.ChainUnknown {
		return entries
	}

	out := make([]audit.ChainEntry, 0, len(entries))

	for i := range entries {
		if !entries[i].Start {
			out = append(out, entries[i])
		}
	}

	return out
}
