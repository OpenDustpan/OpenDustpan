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
// An entry records what happened, for example a refusal or a sign-in, so
// this append has no installation state check: maintenance mode must never
// drop it. The change that an entry describes keeps its own check.
func (s *Store) Append(ctx context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error) {
	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	if e.Namespace != fence.Namespace {
		return audit.Entry{}, fmt.Errorf("%w: entry names %q, fence names %q",
			audit.ErrInvalidEntry, e.Namespace, fence.Namespace)
	}

	var stored audit.Entry

	txErr := s.inControlFencedTx(ctx, fence, func(q *db.Queries) error {
		var err error

		stored, err = appendAuditEntry(ctx, q, e)

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
// An entry records what happened, for example a refusal or a sign-in, so
// this append has no installation state check: maintenance mode must never
// drop it. The change that an entry describes keeps its own check.
func (s *Store) AppendServer(ctx context.Context, e audit.Entry) (audit.Entry, error) {
	if err := validServerEntry(e); err != nil {
		return audit.Entry{}, err
	}

	var stored audit.Entry

	txErr := s.inControlTx(ctx, func(q *db.Queries) error {
		var err error

		stored, err = appendAuditEntry(ctx, q, e)

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
	return s.inAuditedTxOf(ctx, &e, fn)
}

// inAuditedTxOf is inAuditedTx with an entry that fn may change before the
// append, for example to add changes that only the transaction can read. It
// checks the entry again after fn.
func (s *Store) inAuditedTxOf(ctx context.Context, e *audit.Entry, fn func(q *db.Queries) error) error {
	if err := validServerEntry(*e); err != nil {
		return err
	}

	return s.inTx(ctx, func(q *db.Queries) error {
		if err := fn(q); err != nil {
			return err
		}

		if err := validServerEntry(*e); err != nil {
			return err
		}

		if _, err := appendAuditEntry(ctx, q, *e); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}

		return nil
	})
}

// inFencedAuditedTx runs fn and appends e to the chain of the namespace of
// fence in one fenced transaction: the change and its audit entry commit
// together, or neither does (ADR 60, fail closed). An entry that fails
// validation or names another namespace changes nothing. fn may set fields
// of e that only the transaction knows, such as the object ID, before the
// append. The write lock of the
// transaction orders the appends.
//
// The checks run in this order: the entry, then its namespace against the
// fence, then the fence in the transaction, then fn, then the append. An
// invalid entry therefore answers audit.ErrInvalidEntry even when the fence is
// stale, and an error of fn (for example an invalid object) never hides a
// stale fence.
func (s *Store) inFencedAuditedTx(
	ctx context.Context, fence namespace.Fence, e *audit.Entry, fn func(q *db.Queries) error,
) error {
	if err := checkFencedEntry(fence, *e); err != nil {
		return err
	}

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		if err := fn(q); err != nil {
			return err
		}

		if _, err := appendAuditEntry(ctx, q, *e); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}

		return nil
	})
}

// checkFencedEntry returns audit.ErrInvalidEntry when e fails validation or
// names another namespace than fence.
func checkFencedEntry(fence namespace.Fence, e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	if e.Namespace != fence.Namespace {
		return fmt.Errorf("%w: entry names %q, fence names %q",
			audit.ErrInvalidEntry, e.Namespace, fence.Namespace)
	}

	return nil
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

// appendAuditEntry reads the stored chain state and the last entry of the
// scope of e in the same transaction, then inserts e as the next one, with
// its sequence, previous hash and hash. An entry with no ObjectID gets the
// stable ID of the live unit that its Object names. The caller runs it in a
// transaction that orders the appends of the scope. An append computes the
// hash (hashLink) only when the chain of its scope is open. The chain is
// open when the stored state is on. No row also counts as on. The scope
// has no entries, or its last entry holds a hash and is not an end entry
// (Action == audit.ActionEndChain). Otherwise the append writes no hash
// (hashNone). An append in a scope whose chain is closed stays unhashed
// until a start entry. The digest of the start entry covers it.
func appendAuditEntry(ctx context.Context, q *db.Queries, e audit.Entry) (audit.Entry, error) {
	rec, err := readChainRecord(ctx, q)
	if err != nil {
		return audit.Entry{}, err
	}

	placed, err := placeAuditEntry(ctx, q, e)
	if err != nil {
		return audit.Entry{}, err
	}

	mode := hashNone
	if rec.State == audit.ChainStateOn && audit.LastAgrees(placed.last, audit.ChainStateOn) {
		mode = hashLink
	}

	return insertPlacedEntry(ctx, q, placed.entry, placed.prevHash, mode)
}

// hashMode is how insertAuditEntry hashes an entry: not at all, linked to
// the hash of the entry before it, or as the start of a chain with an empty
// previous hash.
type hashMode int

// Hash modes.
const (
	hashNone hashMode = iota
	hashLink
	hashStart
)

// placedEntry is an entry with the fields that the transaction knows,
// plus the last entry of its scope, or nil when the scope holds none.
type placedEntry struct {
	entry    audit.Entry
	prevHash string
	last     *audit.Entry
}

// placeAuditEntry fills the fields of e that the transaction knows: the
// object ID, the failover version of the namespace, the sequence and a time
// that never goes back along the scope. It answers the entry with those
// fields, the hash of the entry before it, or "" for the first entry of the
// scope, and the last entry of the scope, or nil when the scope holds none.
func placeAuditEntry(ctx context.Context, q *db.Queries, e audit.Entry) (placedEntry, error) {
	if e.ObjectID == "" {
		id, err := auditObjectID(ctx, q, e.Object)
		if err != nil {
			return placedEntry{}, err
		}

		e.ObjectID = id
	}

	fenceVersion, err := auditFenceVersion(ctx, q, e.Namespace)
	if err != nil {
		return placedEntry{}, err
	}

	e.FenceVersion = fenceVersion

	var prevHash string

	var last *audit.Entry

	var seq int64 = 1

	row, err := q.LastAuditEntry(ctx, e.Namespace.String())
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return placedEntry{}, fmt.Errorf("read last audit entry: %w", err)
	default:
		prevHash = row.Hash
		seq = row.Sequence + 1

		// Callers take the time before the transaction, and the sequence
		// comes inside it, so two appends to one scope can commit out of
		// time order. The time never goes back along the sequence of a
		// scope: an entry earlier than the last one takes its time.
		if codec.Millis(e.At) < row.AtMs {
			e.At = codec.FromMillis(row.AtMs)
		}

		stored := fromAuditRow(row)
		last = &stored
	}

	e.Sequence = seq

	return placedEntry{entry: e, prevHash: prevHash, last: last}, nil
}

// insertAuditEntry inserts e as the next entry of its scope, hashed as mode
// says.
func insertAuditEntry(
	ctx context.Context, q *db.Queries, e audit.Entry, mode hashMode,
) error {
	placed, err := placeAuditEntry(ctx, q, e)
	if err != nil {
		return err
	}

	_, err = insertPlacedEntry(ctx, q, placed.entry, placed.prevHash, mode)

	return err
}

// insertPlacedEntry hashes placed as mode says and inserts it as the next
// entry of its scope. placed already holds the sequence and the other fields
// that the transaction knows, and prevHash is the hash of the entry before
// it.
func insertPlacedEntry(
	ctx context.Context, q *db.Queries, e audit.Entry, prevHash string, mode hashMode,
) (audit.Entry, error) {
	e.PrevHash, e.Hash = "", ""

	// With the chain off, the entry holds no hash. A start entry begins a
	// chain with an empty previous hash.
	if mode == hashLink {
		e.PrevHash = prevHash
		e.Hash = audit.ComputeHash(e, prevHash)
	}

	if mode == hashStart {
		e.Hash = audit.ComputeHash(e, "")
	}

	insertErr := q.InsertAuditEntry(ctx, db.InsertAuditEntryParams{
		Namespace:     e.Namespace.String(),
		Sequence:      e.Sequence,
		Subject:       e.Subject,
		IdpSubject:    e.IDPSubject,
		TokenID:       e.TokenID,
		Action:        e.Action,
		Object:        e.Object,
		Version:       e.Version,
		ValuePath:     e.ValuePath,
		AtMs:          codec.Millis(e.At),
		SourceIp:      e.SourceIP,
		UserAgent:     e.UserAgent,
		Channel:       string(e.Channel),
		Kind:          string(e.Kind),
		Outcome:       string(e.Outcome),
		RequestID:     e.RequestID,
		PrevHash:      e.PrevHash,
		Hash:          e.Hash,
		ValueCount:    toNullInt64(e.ValueCount),
		Changes:       toChangesText(e.Changes),
		ObjectID:      e.ObjectID,
		FenceVersion:  e.FenceVersion,
		PrincipalKind: e.PrincipalKind,
		JobID:         e.JobID,
		Reason:        e.Reason,
	})
	if insertErr != nil {
		return audit.Entry{}, fmt.Errorf("insert audit entry: %w", insertErr)
	}

	return e, nil
}

// auditFenceVersion answers the failover version of the namespace scope in
// q, read in the transaction of the append, so the hash of the entry covers
// the fence it was written under. ServerScope, and a scope without a
// namespace row, answer 0.
func auditFenceVersion(ctx context.Context, q *db.Queries, scope namespace.Name) (int64, error) {
	if scope == audit.ServerScope {
		return 0, nil
	}

	row, err := q.GetNamespace(ctx, scope.String())
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("read the failover version of %s: %w", scope, err)
	}

	return row.FailoverVersion, nil
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
// field, a broken link, a gap after a deleted entry, or an entry that does
// not fit the timeline of the chain. Each round trip reads at most
// audit.CheckPageSize entries, so a namespace with a large log never grows
// the check without a bound. A namespace with no entries checks as valid.
func (s *Store) Check(ctx context.Context, ns namespace.Name) (audit.CheckResult, error) {
	results, err := s.CheckEach(ctx, []namespace.Name{ns})
	if err != nil {
		return audit.CheckResult{}, err
	}

	if len(results) != 1 {
		return audit.CheckResult{}, fmt.Errorf("check %s: %w", ns, errCheckCount)
	}

	return results[0], nil
}

// CheckEach implements audit.ScopesChecker: it reads the record of the
// chain once and checks each scope, in a read transaction that sees one
// snapshot of the log and takes no write lock.
func (s *Store) CheckEach(ctx context.Context, scopes []namespace.Name) ([]audit.CheckResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]audit.CheckResult, 0, len(scopes))

	err := s.inReadTx(ctx, func(q *db.Queries) error {
		rec, err := readChainRecord(ctx, q)
		if err != nil {
			return err
		}

		for _, ns := range scopes {
			result, err := checkScope(ctx, q, ns, rec)
			if err != nil {
				return err
			}

			out = append(out, result)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// errCheckCount reports a check that answered another count of results
// than of scopes. CheckEach answers one result for each scope.
var errCheckCount = errors.New("the check answered no result")

// checkScope walks the chain of ns in q against the record rec (see
// audit.ChainWalk), from sequence 1, in pages of audit.CheckPageSize.
func checkScope(
	ctx context.Context,
	q *db.Queries,
	ns namespace.Name,
	rec audit.ChainRecord,
) (audit.CheckResult, error) {
	lowest, err := q.MinAuditSequence(ctx, ns.String())
	if err != nil {
		return audit.CheckResult{}, fmt.Errorf("select lowest audit sequence: %w", err)
	}

	// The walk starts at sequence 1, so a row below it would never be
	// checked: it fails the scope.
	if lowest < 1 {
		return audit.CheckResult{Checked: 0, BadSequence: lowest, Unchained: 0, Valid: false}, nil
	}

	start, err := q.LastAuditChainStartSequence(ctx, ns.String())
	if err != nil {
		return audit.CheckResult{}, fmt.Errorf("select audit chain start: %w", err)
	}

	walk := audit.NewChainWalk(audit.ChainScope{Record: rec, Start: start})

	var (
		checked int64
		bad     int64
	)

	err = walkScope(ctx, q, ns, func(row db.AuditEntry) {
		if bad != 0 {
			return
		}

		checked++

		if !walk.StepStored(fromAuditRow(row), row.Changes.String) {
			bad = row.Sequence
		}
	})
	if err != nil {
		return audit.CheckResult{}, err
	}

	if bad == 0 && !walk.Finish() {
		bad = checked
	}

	return audit.CheckResult{Checked: checked, BadSequence: bad, Unchained: walk.Unchained(), Valid: bad == 0}, nil
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
		At:            codec.FromMillis(row.AtMs),
		Namespace:     ns,
		Subject:       row.Subject,
		IDPSubject:    row.IdpSubject,
		TokenID:       row.TokenID,
		Action:        row.Action,
		Object:        row.Object,
		Version:       row.Version,
		ValuePath:     row.ValuePath,
		Changes:       changes,
		ValueCount:    fromNullInt64(row.ValueCount),
		ObjectID:      row.ObjectID,
		SourceIP:      row.SourceIp,
		UserAgent:     row.UserAgent,
		Channel:       audit.Channel(row.Channel),
		Kind:          audit.Kind(row.Kind),
		Outcome:       audit.Outcome(row.Outcome),
		RequestID:     row.RequestID,
		PrevHash:      row.PrevHash,
		Hash:          row.Hash,
		Sequence:      row.Sequence,
		FenceVersion:  row.FenceVersion,
		PrincipalKind: row.PrincipalKind,
		JobID:         row.JobID,
		Reason:        row.Reason,
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

// toChangesText maps the changes of an entry to their canonical JSON, and
// no changes to NULL.
func toChangesText(changes []audit.Change) sql.NullString {
	text := audit.EncodeChanges(changes)

	return sql.NullString{String: text, Valid: text != ""}
}

// updateServerSetting reads the one row of an installation setting with
// read, calls update with it, and writes the new value with write and the
// audit entry of @server that update returns, in one transaction: both
// commit, or neither. An error of update ends the transaction with no
// change, and updateServerSetting returns it as it is.
func updateServerSetting[T any](
	ctx context.Context,
	s *Store,
	read func(*db.Queries) (T, error),
	update func(T) (T, audit.Entry, error),
	write func(*db.Queries, T) error,
) (T, error) {
	var stored T

	err := s.inTx(ctx, func(q *db.Queries) error {
		old, err := read(q)
		if err != nil {
			return err
		}

		value, e, err := update(old)
		if err != nil {
			return err
		}

		if err := validServerEntry(e); err != nil {
			return err
		}

		if err := write(q, value); err != nil {
			return err
		}

		if _, err := appendAuditEntry(ctx, q, e); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}

		stored = value

		return nil
	})
	if err != nil {
		var zero T

		return zero, err
	}

	return stored, nil
}
