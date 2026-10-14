package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var (
	_ audit.Store       = (*Store)(nil)
	_ audit.ServerStore = (*Store)(nil)
)

// Append implements audit.Store: it reads the last hash of the namespace of
// fence and writes the entry in one transaction with the fence check, so the
// appends of one namespace form one sequence. It returns the stored entry
// with its sequence, previous hash and hash.
func (s *Store) Append(ctx context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error) {
	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	if e.Namespace != fence.Namespace {
		return audit.Entry{}, fmt.Errorf("%w: entry names %q, fence names %q",
			audit.ErrInvalidEntry, e.Namespace, fence.Namespace)
	}

	var stored audit.Entry

	txErr := s.inFencedUpdateTx(ctx, fence, func(q *db.Queries) error {
		var err error

		stored, err = s.appendAuditEntry(ctx, q, e)

		return err
	})
	if txErr != nil {
		return audit.Entry{}, txErr
	}

	return stored, nil
}

// AppendServer implements audit.ServerStore: it reads the last hash of
// audit.ServerScope and writes the entry in one transaction, so the appends
// of the scope form one sequence. A transaction-scoped advisory lock orders
// appends that run at the same time, as the namespace row lock does for a
// namespace.
func (s *Store) AppendServer(ctx context.Context, e audit.Entry) (audit.Entry, error) {
	if err := validServerEntry(e); err != nil {
		return audit.Entry{}, err
	}

	var stored audit.Entry

	txErr := s.inTx(ctx, func(q *db.Queries) error {
		if err := q.LockServerAuditScope(ctx); err != nil {
			return fmt.Errorf("lock server audit scope: %w", err)
		}

		var err error

		stored, err = s.appendAuditEntry(ctx, q, e)

		return err
	})
	if txErr != nil {
		return audit.Entry{}, txErr
	}

	return stored, nil
}

// inAuditedTx runs fn and appends e in the scope @server in one
// transaction: the change and its audit entry commit together, or neither
// does (ADR 60, fail closed). An entry that fails validation changes
// nothing. The transaction takes the advisory lock of the scope first, so
// the appends form one sequence and no lock order can deadlock.
func (s *Store) inAuditedTx(ctx context.Context, e audit.Entry, fn func(q *db.Queries) error) error {
	if err := validServerEntry(e); err != nil {
		return err
	}

	return s.inTx(ctx, func(q *db.Queries) error {
		if err := q.LockServerAuditScope(ctx); err != nil {
			return fmt.Errorf("lock server audit scope: %w", err)
		}

		if err := fn(q); err != nil {
			return err
		}

		if _, err := s.appendAuditEntry(ctx, q, e); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}

		return nil
	})
}

// inFencedAuditedTx runs fn and appends e to the chain of the namespace of
// fence in one fenced transaction: the change and its audit entry commit
// together, or neither does (ADR 60, fail closed). An entry that fails
// validation or names another namespace changes nothing. The transaction holds the
// namespace row FOR UPDATE, as Append does, so the appends form one sequence.
func (s *Store) inFencedAuditedTx(
	ctx context.Context, fence namespace.Fence, e audit.Entry, fn func(q *db.Queries) error,
) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	if e.Namespace != fence.Namespace {
		return fmt.Errorf("%w: entry names %q, fence names %q",
			audit.ErrInvalidEntry, e.Namespace, fence.Namespace)
	}

	return s.inFencedUpdateTx(ctx, fence, func(q *db.Queries) error {
		if err := fn(q); err != nil {
			return err
		}

		if _, err := s.appendAuditEntry(ctx, q, e); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}

		return nil
	})
}

// inLockedFencedAuditedTx is inFencedAuditedTx that takes the advisory lock
// of the scope @server first, then the namespace row of fence. A write that
// must be ordered with the writes of the installation (a grant inside a
// namespace, against the delete of its subject or its role) uses it. No
// transaction takes the lock after a namespace row, so the lock order
// cannot deadlock.
func (s *Store) inLockedFencedAuditedTx(
	ctx context.Context, fence namespace.Fence, e audit.Entry, fn func(q *db.Queries) error,
) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	if e.Namespace != fence.Namespace {
		return fmt.Errorf("%w: entry names %q, fence names %q",
			audit.ErrInvalidEntry, e.Namespace, fence.Namespace)
	}

	return s.inTx(ctx, func(q *db.Queries) error {
		if err := q.LockServerAuditScope(ctx); err != nil {
			return fmt.Errorf("lock server audit scope: %w", err)
		}

		if err := checkFenceForUpdate(ctx, q, fence); err != nil {
			return err
		}

		if err := fn(q); err != nil {
			return err
		}

		if _, err := s.appendAuditEntry(ctx, q, e); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}

		return nil
	})
}

// validServerEntry returns audit.ErrInvalidEntry when e fails validation or
// names a scope other than @server.
func validServerEntry(e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	if e.Namespace != audit.ServerScope {
		return fmt.Errorf("%w: entry names %q, want the scope %q",
			audit.ErrInvalidEntry, e.Namespace, audit.ServerScope)
	}

	return nil
}

// appendAuditEntry reads the last entry of the scope of e and inserts e as
// the next one, with its sequence, previous hash and hash. The caller runs
// it in a transaction that orders the appends of the scope. The entry holds
// a hash when the chain is on (ADR 64): the state this handle read at most
// audit.ChainSwitchLag ago, or else the state the transaction reads.
func (s *Store) appendAuditEntry(ctx context.Context, q *db.Queries, e audit.Entry) (audit.Entry, error) {
	chained, err := s.chain.On(func() (audit.ChainState, bool, error) {
		return readChainState(ctx, q)
	})
	if err != nil {
		return audit.Entry{}, fmt.Errorf("read audit chain state: %w", err)
	}

	state := audit.ChainStateOff
	if chained {
		state = audit.ChainStateOn
	}

	return s.insertAuditEntry(ctx, q, e, state)
}

// insertAuditEntry inserts e as the next entry of its scope, with a hash
// only when chain is audit.ChainStateOn.
func (s *Store) insertAuditEntry(
	ctx context.Context, q *db.Queries, e audit.Entry, chain audit.ChainState,
) (audit.Entry, error) {
	var prevHash string

	var seq int64 = 1

	last, err := q.LastAuditEntry(ctx, e.Namespace.String())
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return audit.Entry{}, fmt.Errorf("read last audit entry: %w", err)
	default:
		prevHash = last.Hash
		seq = last.Sequence + 1
	}

	e.Sequence = seq
	e.PrevHash, e.Hash = "", ""

	// With the chain off, the entry holds no hash (ADR 60 decision 17). A
	// chained entry after an unchained one links to an empty previous hash.
	if chain == audit.ChainStateOn {
		e.PrevHash = prevHash
		e.Hash = audit.ComputeHash(e, prevHash)
	}

	insertErr := q.InsertAuditEntry(ctx, db.InsertAuditEntryParams{
		Namespace:  e.Namespace.String(),
		Sequence:   e.Sequence,
		Subject:    e.Subject,
		IdpSubject: e.IDPSubject,
		TokenID:    e.TokenID,
		Action:     e.Action,
		Object:     e.Object,
		Version:    e.Version,
		ValuePath:  e.ValuePath,
		AtMs:       e.AtMs(),
		SourceIp:   e.SourceIP,
		UserAgent:  e.UserAgent,
		Channel:    string(e.Channel),
		Kind:       string(e.Kind),
		Outcome:    string(e.Outcome),
		RequestID:  e.RequestID,
		PrevHash:   e.PrevHash,
		Hash:       e.Hash,
		ValueCount: toInt8(e.ValueCount),
		Changes:    toChangesText(e.Changes),
	})
	if insertErr != nil {
		return audit.Entry{}, fmt.Errorf("insert audit entry: %w", insertErr)
	}

	return e, nil
}

// inFencedUpdateTx runs fn in a transaction that first checks fence, holding
// the namespace row FOR UPDATE for the whole transaction. Audit appends use
// it instead of inFencedTx: FOR SHARE lets two appends read the same last
// entry and collide on the sequence, while FOR UPDATE makes the second wait
// until the first commits, so it reads the new last entry. A promotion (an
// UPDATE of the namespace row) still waits the same way, so the fence still
// holds.
func (s *Store) inFencedUpdateTx(
	ctx context.Context, fence namespace.Fence, fn func(q *db.Queries) error,
) error {
	return s.inTx(ctx, func(q *db.Queries) error {
		if err := checkFenceForUpdate(ctx, q, fence); err != nil {
			return err
		}

		return fn(q)
	})
}

// checkFenceForUpdate holds the namespace row of fence FOR UPDATE and
// checks its failover version. It returns namespace.ErrNotFound for a
// missing namespace and namespace.ErrFenced for a stale version.
func checkFenceForUpdate(ctx context.Context, q *db.Queries, fence namespace.Fence) error {
	version, err := q.GetFailoverVersionForUpdate(ctx, fence.Namespace.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return namespace.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("read failover version: %w", err)
	}

	if version != fence.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}

// List implements audit.Store: one page of the entries of ns, newest first,
// with the total count. A namespace with no entries reads as an empty list.
func (s *Store) List(ctx context.Context, ns namespace.Name, page audit.Page) (audit.Listing, error) {
	p := page.Normalized()

	total, err := s.queries.CountAuditEntries(ctx, ns.String())
	if err != nil {
		return audit.Listing{}, fmt.Errorf("count audit entries: %w", err)
	}

	rows, err := s.queries.ListAuditEntries(ctx, db.ListAuditEntriesParams{
		Namespace: ns.String(),
		Limit:     clampInt32(p.Size),
		Offset:    clampInt32(int(p.Offset())),
	})
	if err != nil {
		return audit.Listing{}, fmt.Errorf("select audit entries: %w", err)
	}

	entries := make([]audit.Entry, 0, len(rows))

	for i := range rows {
		entries = append(entries, fromAuditRow(rows[i]))
	}

	return audit.Listing{Entries: entries, Total: total}, nil
}

// Check implements audit.Store: it walks the chain of ns oldest first in
// bounded pages and reports the first entry that does not match: a changed
// field, a broken link or a gap after a deleted entry. Each round trip reads
// at most audit.CheckPageSize entries, so a namespace with a large log never
// grows the check without a bound. A namespace with no entries checks as
// valid.
func (s *Store) Check(ctx context.Context, ns namespace.Name) (audit.CheckResult, error) {
	lowest, err := s.queries.MinAuditSequence(ctx, ns.String())
	if err != nil {
		return audit.CheckResult{}, fmt.Errorf("select lowest audit sequence: %w", err)
	}

	// The walk starts at sequence 1, so a row below it would never be
	// checked: it fails the scope.
	if lowest < 1 {
		return audit.CheckResult{Checked: 0, BadSequence: lowest, Unchained: 0, Valid: false}, nil
	}

	timeline, err := chainTimeline(ctx, s.queries)
	if err != nil {
		return audit.CheckResult{}, err
	}

	walk := audit.NewChainWalk(timeline)

	var wantSeq int64 = 1

	var checked int64

	for {
		rows, err := s.queries.ListAuditChainPage(ctx, db.ListAuditChainPageParams{
			Namespace: ns.String(),
			Sequence:  wantSeq - 1,
			Limit:     clampInt32(audit.CheckPageSize),
		})
		if err != nil {
			return audit.CheckResult{}, fmt.Errorf("select audit chain: %w", err)
		}

		read := checked + int64(len(rows))

		for i := range rows {
			e := fromAuditRow(rows[i])

			if !walk.StepStored(e, rows[i].Changes.String) {
				return audit.CheckResult{
					Checked: read, BadSequence: e.Sequence, Unchained: walk.Unchained(), Valid: false,
				}, nil
			}

			wantSeq++
		}

		checked += int64(len(rows))

		if len(rows) < audit.CheckPageSize {
			return audit.CheckResult{Checked: checked, BadSequence: 0, Unchained: walk.Unchained(), Valid: true}, nil
		}
	}
}

// fromAuditRow converts a row into a domain entry. The namespace reads back
// without validation: it was validated on write and the foreign key holds.
// A NULL value count reads as nil: only a decrypt call stores a count. A
// NULL changes column reads as nil. The changes decode leniently, for
// display: a text that does not decode reads as nil, so a list always
// answers the entry, and only the chain check reports the changed text.
func fromAuditRow(row db.AuditEntry) audit.Entry {
	ns := namespace.Name(row.Namespace)

	changes, err := audit.DecodeChanges(row.Changes.String)
	if err != nil {
		changes = nil
	}

	return audit.Entry{
		At:         codec.FromMillis(row.AtMs),
		Namespace:  ns,
		Subject:    row.Subject,
		IDPSubject: row.IdpSubject,
		TokenID:    row.TokenID,
		Action:     row.Action,
		Object:     row.Object,
		Version:    row.Version,
		ValuePath:  row.ValuePath,
		Changes:    changes,
		ValueCount: fromInt8(row.ValueCount),
		SourceIP:   row.SourceIp,
		UserAgent:  row.UserAgent,
		Channel:    audit.Channel(row.Channel),
		Kind:       audit.Kind(row.Kind),
		Outcome:    audit.Outcome(row.Outcome),
		RequestID:  row.RequestID,
		PrevHash:   row.PrevHash,
		Hash:       row.Hash,
		Sequence:   row.Sequence,
	}
}

// toInt8 maps a nil count to NULL: only a decrypt call stores a count.
func toInt8(count *int64) pgtype.Int8 {
	if count == nil {
		return pgtype.Int8{Int64: 0, Valid: false}
	}

	return pgtype.Int8{Int64: *count, Valid: true}
}

// fromInt8 maps a NULL count to nil.
func fromInt8(count pgtype.Int8) *int64 {
	if !count.Valid {
		return nil
	}

	out := count.Int64

	return &out
}

// toChangesText maps the changes of an entry to their canonical JSON, and
// no changes to NULL.
func toChangesText(changes []audit.Change) pgtype.Text {
	text := audit.EncodeChanges(changes)

	return pgtype.Text{String: text, Valid: text != ""}
}
