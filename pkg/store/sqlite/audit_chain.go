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

var (
	_ audit.ChainSwitch    = (*Store)(nil)
	_ audit.RestoreChecker = (*Store)(nil)
)

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
		return audit.ChainRecord{Restore: audit.NoRestore(), State: audit.ChainUnknown, EverChained: false}, nil
	}

	if err != nil {
		return audit.ChainRecord{}, fmt.Errorf("read audit chain state: %w", err)
	}

	return recordOf(row), nil
}

// RecordChain implements audit.ChainSwitch. The write lock of the
// transaction orders the appends of the server scope.
func (s *Store) RecordChain(ctx context.Context, w audit.ChainWrite) error {
	var ever, applied int64
	if w.Record.EverChained {
		ever = 1
	}

	if w.Record.Restore.Applied {
		applied = 1
	}

	err := s.inTx(ctx, func(q *db.Queries) error {
		// Two instances that start at the same time read the same record.
		// The one that commits first changes it, so the other one must
		// read it again and decide again.
		current, err := readChainRecord(ctx, q)
		if err != nil {
			return err
		}

		if current != w.Read {
			return fmt.Errorf("record audit chain: %w", audit.ErrChainRecordChanged)
		}

		if err := s.insertChainEntries(ctx, q, w); err != nil {
			return err
		}

		params := db.SetAuditChainStateParams{
			State:              string(w.Record.State),
			EverChained:        ever,
			RestoreID:          w.Record.Restore.ID,
			RestoreBackupID:    w.Record.Restore.BackupID,
			RestoreRequestedBy: w.Record.Restore.RequestedBy,
			RestoreApplied:     applied,
		}
		if err := q.SetAuditChainState(ctx, params); err != nil {
			return fmt.Errorf("save audit chain state: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	s.chain.Set(w.Record.State == audit.ChainStateOn)

	return nil
}

// insertChainEntries appends the switch entries of w in q, each with a hash
// only when it is Chained, at the time that w.Now gives in the transaction.
func (s *Store) insertChainEntries(ctx context.Context, q *db.Queries, w audit.ChainWrite) error {
	var at time.Time
	if w.Now != nil {
		at = w.Now()
	}

	for i := range w.Entries {
		e := w.Entries[i].Entry
		if w.Now != nil {
			e.At = at
		}

		if err := validServerEntry(e); err != nil {
			return err
		}

		chain := audit.ChainStateOff
		if w.Entries[i].Chained {
			chain = audit.ChainStateOn
		}

		if _, err := s.insertAuditEntry(ctx, q, e, chain); err != nil {
			return fmt.Errorf("append audit chain entry: %w", err)
		}
	}

	return nil
}

// readChainRecord reads the record of the chain in q. A missing row reads
// as no record.
func readChainRecord(ctx context.Context, q *db.Queries) (audit.ChainRecord, error) {
	row, err := q.GetAuditChainState(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return audit.ChainRecord{Restore: audit.NoRestore(), State: audit.ChainUnknown, EverChained: false}, nil
	}

	if err != nil {
		return audit.ChainRecord{}, fmt.Errorf("read audit chain state: %w", err)
	}

	return recordOf(row), nil
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

// timelineRule answers the timeline of a builder against the record: the
// rule of the check, or the rule of the check of a restored log.
type timelineRule func(b *audit.TimelineBuilder, rec audit.ChainRecord) audit.ChainTimeline

// chainTimeline answers the timeline of the chain for the chain check. It
// walks the server scope from sequence 1 up to its last switch entry in
// bounded pages, so only switch entries that the walk accepts in the chain
// count (see audit.TimelineBuilder). A row of the server scope below
// sequence 1 breaks the timeline.
func chainTimeline(ctx context.Context, q *db.Queries, build timelineRule) (audit.ChainTimeline, error) {
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

	return build(b, rec), nil
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

// recordOf maps a row of audit_chain_state to the chain record.
func recordOf(row db.GetAuditChainStateRow) audit.ChainRecord {
	return audit.ChainRecord{
		Restore: audit.ChainRestoreMark{
			ID:          row.RestoreID,
			BackupID:    row.RestoreBackupID,
			RequestedBy: row.RestoreRequestedBy,
			Applied:     row.RestoreApplied != 0,
		},
		State:       audit.ChainState(row.State),
		EverChained: row.EverChained != 0,
	}
}
