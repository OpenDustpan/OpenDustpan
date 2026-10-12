package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ audit.Store = (*Store)(nil)

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
		var prevHash string

		var seq int64 = 1

		last, err := q.LastAuditEntry(ctx, fence.Namespace.String())
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("read last audit entry: %w", err)
		default:
			prevHash = last.Hash
			seq = last.Sequence + 1
		}

		e.Sequence = seq
		e.PrevHash = prevHash
		e.Hash = audit.ComputeHash(e, prevHash)

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
		})
		if insertErr != nil {
			return fmt.Errorf("insert audit entry: %w", insertErr)
		}

		stored = e

		return nil
	})
	if txErr != nil {
		return audit.Entry{}, txErr
	}

	return stored, nil
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

		return fn(q)
	})
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
	var prevHash string

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

			if e.Sequence != wantSeq || e.PrevHash != prevHash {
				return audit.CheckResult{Checked: read, BadSequence: e.Sequence, Valid: false}, nil
			}

			if got := audit.ComputeHash(e, e.PrevHash); got != e.Hash {
				return audit.CheckResult{Checked: read, BadSequence: e.Sequence, Valid: false}, nil
			}

			prevHash = e.Hash
			wantSeq++
		}

		checked += int64(len(rows))

		if len(rows) < audit.CheckPageSize {
			return audit.CheckResult{Checked: checked, BadSequence: 0, Valid: true}, nil
		}
	}
}

// fromAuditRow converts a row into a domain entry. The namespace reads back
// without validation: it was validated on write and the foreign key holds.
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
