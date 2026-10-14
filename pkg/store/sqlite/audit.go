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

	txErr := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
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
// of the scope form one sequence. The write lock of SQLite, which each
// transaction takes at its start, orders appends that run at the same time.
func (s *Store) AppendServer(ctx context.Context, e audit.Entry) (audit.Entry, error) {
	if err := validServerEntry(e); err != nil {
		return audit.Entry{}, err
	}

	var stored audit.Entry

	txErr := s.inTx(ctx, func(q *db.Queries) error {
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
// nothing. The write lock of the transaction orders the appends.
func (s *Store) inAuditedTx(ctx context.Context, e audit.Entry, fn func(q *db.Queries) error) error {
	if err := validServerEntry(e); err != nil {
		return err
	}

	return s.inTx(ctx, func(q *db.Queries) error {
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
// validation or names another namespace changes nothing. The write lock of the
// transaction orders the appends.
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

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		if err := fn(q); err != nil {
			return err
		}

		if _, err := appendAuditEntry(ctx, q, e); err != nil {
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
// it in a transaction that orders the appends of the scope.
func (s *Store) appendAuditEntry(ctx context.Context, q *db.Queries, e audit.Entry) (audit.Entry, error) {
	var prevHash string

	var seq int64 = 1

	last, err := q.LastAuditEntry(ctx, e.Namespace.String())
	switch {
	case errors.Is(err, sql.ErrNoRows):
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
	if !s.chainOff.Load() {
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
		AtMs:       codec.Millis(e.At),
		SourceIp:   e.SourceIP,
		UserAgent:  e.UserAgent,
		Channel:    string(e.Channel),
		Kind:       string(e.Kind),
		Outcome:    string(e.Outcome),
		RequestID:  e.RequestID,
		PrevHash:   e.PrevHash,
		Hash:       e.Hash,
		ValueCount: toNullInt64(e.ValueCount),
	})
	if insertErr != nil {
		return audit.Entry{}, fmt.Errorf("insert audit entry: %w", insertErr)
	}

	return e, nil
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
		Limit:     int64(p.Size),
		Offset:    p.Offset(),
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
	walk := audit.NewChainWalk()

	var wantSeq int64 = 1

	var checked int64

	for {
		rows, err := s.queries.ListAuditChainPage(ctx, db.ListAuditChainPageParams{
			Namespace: ns.String(),
			Sequence:  wantSeq - 1,
			Limit:     audit.CheckPageSize,
		})
		if err != nil {
			return audit.CheckResult{}, fmt.Errorf("select audit chain: %w", err)
		}

		read := checked + int64(len(rows))

		for i := range rows {
			e := fromAuditRow(rows[i])

			if !walk.Step(e) {
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
// A NULL value count reads as nil: only a decrypt call stores a count.
func fromAuditRow(row db.AuditEntry) audit.Entry {
	ns := namespace.Name(row.Namespace)

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
		ValueCount: fromNullInt64(row.ValueCount),
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

// toNullInt64 maps a nil count to NULL: only a decrypt call stores a count.
func toNullInt64(count *int64) sql.NullInt64 {
	if count == nil {
		return sql.NullInt64{Int64: 0, Valid: false}
	}

	return sql.NullInt64{Int64: *count, Valid: true}
}

// fromNullInt64 maps a NULL count to nil.
func fromNullInt64(count sql.NullInt64) *int64 {
	if !count.Valid {
		return nil
	}

	out := count.Int64

	return &out
}
