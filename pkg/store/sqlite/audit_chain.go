package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ audit.ChainStore = (*Store)(nil)

// SetChain implements audit.ChainStore for later appends of this handle.
func (s *Store) SetChain(on bool) {
	s.chainOff.Store(!on)
}

// ChainRecord implements audit.ChainStore. A missing row reads as the chain
// on.
func (s *Store) ChainRecord(ctx context.Context) (audit.ChainRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return readChainRecord(ctx, s.queries)
}

// SaveChainRecord implements audit.ChainStore.
func (s *Store) SaveChainRecord(ctx context.Context, rec audit.ChainRecord) error {
	return s.inTx(ctx, func(q *db.Queries) error {
		return saveChainRecord(ctx, q, rec)
	})
}

// StartChain implements audit.ChainStore: it reads the earlier entries of
// the scope for the digest and appends the start entry in the same
// transaction, so no append lands in between.
func (s *Store) StartChain(ctx context.Context, fence namespace.Fence, e audit.Entry) (bool, error) {
	return s.appendChainMarker(ctx, fence, e, audit.ChainStateOn, func(q *db.Queries, e audit.Entry) error {
		var digest audit.Digest

		add := func(row db.AuditEntry) { digest.Add(fromAuditRow(row), row.Changes.String) }
		if err := walkScope(ctx, q, e.Namespace, add); err != nil {
			return err
		}

		e.Changes = audit.StartChanges(&digest)

		_, err := s.insertAuditEntry(ctx, q, e, hashStart)

		return err
	})
}

// EndChain implements audit.ChainStore.
func (s *Store) EndChain(ctx context.Context, fence namespace.Fence, e audit.Entry) (bool, error) {
	return s.appendChainMarker(ctx, fence, e, audit.ChainStateOff, func(q *db.Queries, e audit.Entry) error {
		_, err := s.insertAuditEntry(ctx, q, e, hashLink)

		return err
	})
}

// LastEntry implements audit.ChainStore.
func (s *Store) LastEntry(ctx context.Context, scope namespace.Name) (audit.Entry, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var none audit.Entry

	last, err := lastEntry(ctx, s.queries, scope)
	if err != nil || last == nil {
		return none, false, err
	}

	return *last, true, nil
}

// appendChainMarker validates e and runs insert in one transaction with the
// check of fence, unless the scope holds no entry or its last entry already
// agrees with state. It reports whether it ran insert. A fence of the server scope checks nothing.
func (s *Store) appendChainMarker(
	ctx context.Context, fence namespace.Fence, e audit.Entry, state audit.ChainState,
	insert func(q *db.Queries, e audit.Entry) error,
) (bool, error) {
	if err := e.Validate(); err != nil {
		return false, fmt.Errorf("audit entry: %w", err)
	}

	if e.Namespace != fence.Namespace {
		return false, fmt.Errorf("%w: entry names %q, fence names %q",
			audit.ErrInvalidEntry, e.Namespace, fence.Namespace)
	}

	var wrote bool

	run := func(q *db.Queries) error {
		last, err := lastEntry(ctx, q, e.Namespace)
		if err != nil {
			return err
		}

		if audit.LastAgrees(last, state) {
			return nil
		}

		wrote = true

		return insert(q, e)
	}

	var err error
	if fence.Namespace == audit.ServerScope {
		err = s.inTx(ctx, run)
	} else {
		err = s.inFencedTx(ctx, fence, run)
	}

	if err != nil {
		return false, err
	}

	return wrote, nil
}

// lastEntry answers the entry with the highest sequence of scope in q, or
// nil when the scope holds none.
func lastEntry(ctx context.Context, q *db.Queries, scope namespace.Name) (*audit.Entry, error) {
	row, err := q.LastAuditEntry(ctx, scope.String())
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // A scope without entries has no last entry.
	}

	if err != nil {
		return nil, fmt.Errorf("read last audit entry: %w", err)
	}

	e := fromAuditRow(row)

	return &e, nil
}

// walkScope calls fn for each entry of scope in q in order of sequence, in
// pages of audit.CheckPageSize.
func walkScope(ctx context.Context, q *db.Queries, scope namespace.Name, fn func(row db.AuditEntry)) error {
	var after int64

	for {
		rows, err := q.ListAuditChainPage(ctx, db.ListAuditChainPageParams{
			Namespace: scope.String(),
			Sequence:  after,
			Limit:     audit.CheckPageSize,
		})
		if err != nil {
			return fmt.Errorf("select audit chain: %w", err)
		}

		for i := range rows {
			fn(rows[i])
			after = rows[i].Sequence
		}

		if len(rows) < audit.CheckPageSize {
			return nil
		}
	}
}

// readChainRecord reads the record of the chain in q. A missing row reads
// as the chain on.
func readChainRecord(ctx context.Context, q *db.Queries) (audit.ChainRecord, error) {
	row, err := q.GetAuditChainState(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return audit.ChainRecord{
			ChangedAt: codec.FromMillis(0), ChangedBy: "", State: audit.ChainStateOn,
			Pending: false, LogReplaced: false,
		}, nil
	}

	if err != nil {
		return audit.ChainRecord{}, fmt.Errorf("read audit chain state: %w", err)
	}

	return audit.ChainRecord{
		ChangedAt:   codec.FromMillis(row.ChangedAtMs),
		ChangedBy:   row.ChangedBy,
		State:       audit.ChainState(row.State),
		Pending:     row.Pending != 0,
		LogReplaced: row.LogReplaced != 0,
	}, nil
}

// saveChainRecord stores rec in q.
func saveChainRecord(ctx context.Context, q *db.Queries, rec audit.ChainRecord) error {
	var pending, replaced int64
	if rec.Pending {
		pending = 1
	}

	if rec.LogReplaced {
		replaced = 1
	}

	err := q.SetAuditChainState(ctx, db.SetAuditChainStateParams{
		State:       string(rec.State),
		ChangedAtMs: codec.Millis(rec.ChangedAt),
		ChangedBy:   rec.ChangedBy,
		Pending:     pending,
		LogReplaced: replaced,
	})
	if err != nil {
		return fmt.Errorf("save audit chain state: %w", err)
	}

	return nil
}
