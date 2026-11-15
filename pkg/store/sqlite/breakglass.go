package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/calendar"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/breakglass"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// BreakGlass returns the store of break-glass reviews. Each write checks the
// fence of the scope of its review in its transaction, and a review of
// audit.ServerScope carries the zero fence. Open and Record append their audit
// entry in the same transaction, so a failed write changes nothing.
func (s *Store) BreakGlass() BreakGlassStore {
	return BreakGlassStore{s: s}
}

// BreakGlassStore is breakglass.Store over the database of a Store.
type BreakGlassStore struct {
	s *Store
}

var _ breakglass.Store = BreakGlassStore{s: nil}

// breakGlassColumns lists the columns of a review row, in schema order, for
// the list query that the adapter writes by hand.
const breakGlassColumns = `id, use_kind, use_ref, use_scope, use_object, use_subject, use_people, use_reason,
	use_at_ms, use_until_ms, state, created_at_ms, due_at_ms, calendar, calendar_from_namespace,
	calendar_from_group, step, next_notice_at_ms, verdict_reviewer, verdict_note, verdict_auth,
	verdict_auth_at_ms, verdict_reviewed_at_ms, verdict_act_as, version`

// Open implements breakglass.Store. An open that runs again for the same kind
// and reference returns the stored review and appends no second audit entry.
func (g BreakGlassStore) Open(
	ctx context.Context, r breakglass.Review, fence namespace.Fence, e audit.Entry,
) (breakglass.Review, error) {
	// An error returns the zero review, so that no caller reads a review from
	// it.
	var none breakglass.Review

	if err := checkBreakGlassOpen(r, fence, e); err != nil {
		return none, err
	}

	g.s.mu.RLock()
	defer g.s.mu.RUnlock()

	var out breakglass.Review

	err := g.breakGlassTx(ctx, fence, func(q *db.Queries) error {
		stored, openErr := breakGlassOpen(ctx, q, r, fence, e)
		if openErr != nil {
			return openErr
		}

		out = stored

		return nil
	})
	if err != nil {
		return none, err
	}

	return out, nil
}

// checkBreakGlassOpen checks the review, the fence and the audit entry of an
// open before its transaction opens.
func checkBreakGlassOpen(r breakglass.Review, fence namespace.Fence, e audit.Entry) error {
	if err := r.ValidateOpen(); err != nil {
		return fmt.Errorf("open review: %w", err)
	}

	// Defense in depth: the transaction below checks the fence against the
	// scope of the stored review. This check stays so that a review of one
	// scope cannot be opened under the fence of another scope, whatever a
	// later change does to the transaction.
	if !breakGlassFenceMatches(fence, r.Use.Scope) {
		return fmt.Errorf(
			"%w: the fence of %s does not belong to the scope %s",
			breakglass.ErrInvalid, fence.Namespace, r.Use.Scope,
		)
	}

	// Defense in depth: the transaction binds the fence to the scope of the
	// review, and breakGlassCurrent binds the fence to the scope of the
	// stored review. This check stays so that the entry and the review are
	// bound to one scope as well, and a review never appends to the chain of
	// another scope.
	if e.Namespace != r.Use.Scope {
		return fmt.Errorf(
			"%w: the audit entry is in %s, the review in %s", breakglass.ErrInvalid, e.Namespace, r.Use.Scope,
		)
	}

	if err := e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	return nil
}

// breakGlassOpen opens r in the transaction of q: it returns the stored review
// of the use when the use already holds one, and otherwise it inserts the new
// review and appends e.
func breakGlassOpen(
	ctx context.Context, q *db.Queries, r breakglass.Review, fence namespace.Fence, e audit.Entry,
) (breakglass.Review, error) {
	// An error returns the zero review, so that no caller reads a review from
	// it.
	var none breakglass.Review

	existing, found, err := breakGlassByUse(ctx, q, r.Use)
	if err != nil {
		return none, err
	}

	if !found {
		return breakGlassInsertNew(ctx, q, r, e)
	}

	// The use already holds a review. A repeated open returns it unchanged,
	// and the entry of the first open stays the only one.
	if existing.Use.Scope != r.Use.Scope || !breakGlassFenceMatches(fence, existing.Use.Scope) {
		return none, fmt.Errorf(
			"%w: the review %s is in the scope %s", breakglass.ErrInvalid, existing.ID, existing.Use.Scope,
		)
	}

	return existing, nil
}

// breakGlassInsertNew inserts the new review r and appends e to the chain of
// its scope in the transaction of q.
func breakGlassInsertNew(
	ctx context.Context, q *db.Queries, r breakglass.Review, e audit.Entry,
) (breakglass.Review, error) {
	// An error returns the zero review, so that no caller reads a review from
	// it.
	var none breakglass.Review

	if err := breakGlassIDFree(ctx, q, r.ID); err != nil {
		return none, err
	}

	insert, err := breakGlassInsert(r)
	if err != nil {
		return none, err
	}

	n, err := q.InsertBreakGlassReview(ctx, insert)
	if err != nil {
		return none, fmt.Errorf("insert break-glass review: %w", err)
	}

	if n == 0 {
		return none, fmt.Errorf("%w: another review holds the use of %s", breakglass.ErrInvalid, r.ID)
	}

	// The read of the stored row answers exactly what a later Get answers,
	// with the times of the row and the version 1.
	stored, err := breakGlassStored(ctx, q, r.ID)
	if err != nil {
		return none, err
	}

	if err := appendBreakGlassEntry(ctx, q, e); err != nil {
		return none, err
	}

	return stored, nil
}

// breakGlassIDFree returns ErrInvalid when another review holds id.
func breakGlassIDFree(ctx context.Context, q *db.Queries, id string) error {
	_, err := q.GetBreakGlassReview(ctx, id)

	switch {
	case err == nil:
		return fmt.Errorf("%w: another review holds the ID %s", breakglass.ErrInvalid, id)
	case errors.Is(err, sql.ErrNoRows):
		return nil
	default:
		return fmt.Errorf("select break-glass review: %w", err)
	}
}

// Get implements breakglass.Store.
func (g BreakGlassStore) Get(ctx context.Context, scope namespace.Name, id string) (breakglass.Review, error) {
	// An error returns the zero review, so that no caller reads a review from
	// it.
	var none breakglass.Review

	g.s.mu.RLock()
	defer g.s.mu.RUnlock()

	row, err := g.s.queries.GetBreakGlassReview(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return none, fmt.Errorf("get %s: %w", id, breakglass.ErrNotFound)
	}

	if err != nil {
		return none, fmt.Errorf("select break-glass review: %w", err)
	}

	r, err := breakGlassReviewOf(row)
	if err != nil {
		return none, fmt.Errorf("review %s: %w", id, err)
	}

	// Defense in depth: a list query filters the scope in SQL. This check
	// stays so that a read in one scope never tells that a review of another
	// scope exists.
	if r.Use.Scope != scope {
		return none, fmt.Errorf("get %s in %s: %w", id, scope, breakglass.ErrNotFound)
	}

	return r, nil
}

// List implements breakglass.Store.
func (g BreakGlassStore) List(ctx context.Context, f breakglass.Filter) ([]breakglass.Review, error) {
	query, args := breakGlassListQuery(f)

	g.s.mu.RLock()
	defer g.s.mu.RUnlock()

	rows, err := g.s.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select break-glass reviews: %w", err)
	}

	return scanBreakGlassReviews(rows)
}

// scanBreakGlassReviews reads every row of a break-glass review query into
// reviews and closes rows.
func scanBreakGlassReviews(rows *sql.Rows) ([]breakglass.Review, error) {
	out, err := readBreakGlassReviews(rows)
	closeErr := rows.Close()

	if err != nil {
		return nil, err
	}

	if closeErr != nil {
		return nil, fmt.Errorf("close break-glass reviews: %w", closeErr)
	}

	return out, nil
}

// readBreakGlassReviews reads every row of a break-glass review query into
// reviews.
func readBreakGlassReviews(rows *sql.Rows) ([]breakglass.Review, error) {
	out := []breakglass.Review{}

	for rows.Next() {
		row, err := breakGlassRow(rows)
		if err != nil {
			return nil, err
		}

		r, err := breakGlassReviewOf(row)
		if err != nil {
			return nil, fmt.Errorf("review %s: %w", row.ID, err)
		}

		out = append(out, r)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("select break-glass reviews: %w", rowsErr)
	}

	return out, nil
}

// Record implements breakglass.Store. It checks the verdict against the stored
// review, not against the review of the caller, and appends the entry e in the
// transaction of the write.
func (g BreakGlassStore) Record(
	ctx context.Context, r breakglass.Review, fence namespace.Fence, e audit.Entry,
) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	g.s.mu.RLock()
	defer g.s.mu.RUnlock()

	return g.breakGlassTx(ctx, fence, func(q *db.Queries) error {
		stored, err := breakGlassCurrent(ctx, q, r, fence)
		if err != nil {
			return err
		}

		return breakGlassRecordVerdict(ctx, q, stored, r, e)
	})
}

// breakGlassRecordVerdict stores the verdict of r on the stored review in the
// transaction of q, and appends e to the chain of the scope.
func breakGlassRecordVerdict(
	ctx context.Context, q *db.Queries, stored, r breakglass.Review, e audit.Entry,
) error {
	// Defense in depth: the transaction binds the fence to the scope of
	// the stored review. This check stays so that the entry and the
	// stored review are bound to one scope as well, and a verdict never
	// appends to the chain of another scope.
	if e.Namespace != stored.Use.Scope {
		return fmt.Errorf(
			"%w: the audit entry is in %s, the review in %s",
			breakglass.ErrInvalid, e.Namespace, stored.Use.Scope,
		)
	}

	if err := breakglass.CheckVerdict(stored, r.State, r.Verdict); err != nil {
		return fmt.Errorf("record %s: %w", r.ID, err)
	}

	if err := breakGlassCheckReviewedAt(ctx, q, r); err != nil {
		return err
	}

	actAs, err := jsonList(r.Verdict.ActAs)
	if err != nil {
		return err
	}

	n, err := q.UpdateBreakGlassReviewVerdict(ctx, db.UpdateBreakGlassReviewVerdictParams{
		State:               string(r.State),
		VerdictReviewer:     r.Verdict.Reviewer,
		VerdictNote:         r.Verdict.Note,
		VerdictAuth:         string(r.Verdict.Auth),
		VerdictAuthAtMs:     breakGlassMillis(r.Verdict.AuthAt),
		VerdictReviewedAtMs: breakGlassMillis(r.Verdict.ReviewedAt),
		VerdictActAs:        actAs,
		Version:             stored.Version + 1,
		ID:                  r.ID,
		PrevVersion:         stored.Version,
	})
	if err != nil {
		return fmt.Errorf("update break-glass review: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("record %s: %w", r.ID, breakglass.ErrConflict)
	}

	return appendBreakGlassEntry(ctx, q, e)
}

// breakGlassCheckReviewedAt returns ErrInvalid for a verdict at a time after
// the time of the transaction plus the maximum clock skew. The time comes from
// the clock of the store, so the verdict of a caller with a fast clock is
// refused.
func breakGlassCheckReviewedAt(ctx context.Context, q *db.Queries, r breakglass.Review) error {
	txTime, err := storeNow(ctx, q)
	if err != nil {
		return err
	}

	if r.Verdict.ReviewedAt.After(codec.FromMillis(txTime).Add(breakglass.MaxClockSkew)) {
		return fmt.Errorf("%w: the verdict of %s is after the time of the transaction", breakglass.ErrInvalid, r.ID)
	}

	return nil
}

// Advance implements breakglass.Store. It writes no audit entry: a notice is
// not an action of a person.
func (g BreakGlassStore) Advance(ctx context.Context, r breakglass.Review, fence namespace.Fence) error {
	g.s.mu.RLock()
	defer g.s.mu.RUnlock()

	return g.breakGlassTx(ctx, fence, func(q *db.Queries) error {
		stored, err := breakGlassCurrent(ctx, q, r, fence)
		if err != nil {
			return err
		}

		n, err := q.UpdateBreakGlassReviewStep(ctx, db.UpdateBreakGlassReviewStepParams{
			Step:           int64(r.Step),
			NextNoticeAtMs: breakGlassMillis(r.NextNoticeAt),
			Version:        stored.Version + 1,
			ID:             r.ID,
			PrevVersion:    stored.Version,
		})
		if err != nil {
			return fmt.Errorf("update break-glass review: %w", err)
		}

		if n == 0 {
			return fmt.Errorf("advance %s: %w", r.ID, breakglass.ErrConflict)
		}

		return nil
	})
}

// Due implements breakglass.Store: the pending reviews whose next notice is at
// or before now, oldest first, at most limit of them.
func (g BreakGlassStore) Due(ctx context.Context, now time.Time, limit int) ([]breakglass.Review, error) {
	// A limit below one selects no review: the port answers "at most limit".
	if limit < 0 {
		limit = 0
	}

	g.s.mu.RLock()
	defer g.s.mu.RUnlock()

	rows, err := g.s.queries.DueBreakGlassReviews(ctx, db.DueBreakGlassReviewsParams{
		NowMs:   breakGlassMillis(now),
		MaxRows: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("select due break-glass reviews: %w", err)
	}

	out := make([]breakglass.Review, 0, len(rows))

	for i := range rows {
		r, reviewErr := breakGlassReviewOf(rows[i])
		if reviewErr != nil {
			return nil, fmt.Errorf("review %s: %w", rows[i].ID, reviewErr)
		}

		out = append(out, r)
	}

	return out, nil
}

// breakGlassTx runs fn in a transaction that checks fence: the failover
// version of its namespace for a namespace review, and nothing for a review of
// audit.ServerScope, which carries the zero fence.
func (g BreakGlassStore) breakGlassTx(
	ctx context.Context, fence namespace.Fence, fn func(q *db.Queries) error,
) error {
	if fence.Namespace == "" {
		return g.s.inTx(ctx, fn)
	}

	return g.s.inFencedTx(ctx, fence, fn)
}

// breakGlassFenceMatches reports whether fence is the fence of a write in
// scope: the zero fence for audit.ServerScope, and the fence of the namespace
// itself for a namespace.
func breakGlassFenceMatches(fence namespace.Fence, scope namespace.Name) bool {
	if scope == audit.ServerScope {
		return fence == namespace.Fence{Namespace: "", FailoverVersion: 0}
	}

	return fence.Namespace == scope
}

// breakGlassCurrent returns the stored review of r and checks that fence
// belongs to its scope. It returns ErrNotFound for an unknown review and for a
// fence of another scope, so that a write does not tell that a review exists
// in another scope, and ErrConflict for an older version.
func breakGlassCurrent(
	ctx context.Context, q *db.Queries, r breakglass.Review, fence namespace.Fence,
) (breakglass.Review, error) {
	// An error returns the zero review, so that no caller reads a review from
	// it.
	var none breakglass.Review

	row, err := q.GetBreakGlassReview(ctx, r.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return none, fmt.Errorf("review %s: %w", r.ID, breakglass.ErrNotFound)
	}

	if err != nil {
		return none, fmt.Errorf("select break-glass review: %w", err)
	}

	stored, err := breakGlassReviewOf(row)
	if err != nil {
		return none, fmt.Errorf("review %s: %w", r.ID, err)
	}

	// Defense in depth: the transaction of the caller checked the fence of
	// its namespace. This check stays so that a write with a fence of another
	// scope cannot change a review, whatever the caller sends.
	if !breakGlassFenceMatches(fence, stored.Use.Scope) {
		return none, fmt.Errorf("review %s: %w", r.ID, breakglass.ErrNotFound)
	}

	if stored.Version != r.Version {
		return none, fmt.Errorf("review %s: %w", r.ID, breakglass.ErrConflict)
	}

	return stored, nil
}

// breakGlassStored reads the stored review of id in the transaction of q.
func breakGlassStored(ctx context.Context, q *db.Queries, id string) (breakglass.Review, error) {
	// An error returns the zero review, so that no caller reads a review from
	// it.
	var none breakglass.Review

	row, err := q.GetBreakGlassReview(ctx, id)
	if err != nil {
		return none, fmt.Errorf("select the stored break-glass review: %w", err)
	}

	stored, err := breakGlassReviewOf(row)
	if err != nil {
		return none, fmt.Errorf("review %s: %w", id, err)
	}

	return stored, nil
}

// breakGlassByUse returns the stored review of the kind and the reference of u
// and whether the store holds one.
func breakGlassByUse(
	ctx context.Context, q *db.Queries, u breakglass.Use,
) (breakglass.Review, bool, error) {
	// An error returns the zero review, so that no caller reads a review from
	// it.
	var none breakglass.Review

	row, err := q.GetBreakGlassReviewByUse(ctx, db.GetBreakGlassReviewByUseParams{
		UseKind: string(u.Kind),
		UseRef:  u.Ref,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("select the break-glass review of the use: %w", err)
	}

	r, err := breakGlassReviewOf(row)
	if err != nil {
		return none, false, fmt.Errorf("review %s: %w", row.ID, err)
	}

	return r, true, nil
}

// breakGlassInsert returns the insert of the new review r, with the version 1
// of a new review.
func breakGlassInsert(r breakglass.Review) (db.InsertBreakGlassReviewParams, error) {
	people, err := jsonList(r.Use.People)
	if err != nil {
		return db.InsertBreakGlassReviewParams{}, err
	}

	actAs, err := jsonList(r.Verdict.ActAs)
	if err != nil {
		return db.InsertBreakGlassReviewParams{}, err
	}

	return db.InsertBreakGlassReviewParams{
		ID:                    r.ID,
		UseKind:               string(r.Use.Kind),
		UseRef:                r.Use.Ref,
		UseScope:              r.Use.Scope.String(),
		UseObject:             r.Use.Object,
		UseSubject:            r.Use.Subject,
		UsePeople:             people,
		UseReason:             r.Use.Reason,
		UseAtMs:               breakGlassMillis(r.Use.At),
		UseUntilMs:            breakGlassMillis(r.Use.Until),
		State:                 string(r.State),
		CreatedAtMs:           breakGlassMillis(r.CreatedAt),
		DueAtMs:               breakGlassMillis(r.DueAt),
		Calendar:              string(r.Calendar),
		CalendarFromNamespace: r.CalendarFrom.Namespace.String(),
		CalendarFromGroup:     r.CalendarFrom.Group,
		Step:                  int64(r.Step),
		NextNoticeAtMs:        breakGlassMillis(r.NextNoticeAt),
		VerdictReviewer:       r.Verdict.Reviewer,
		VerdictNote:           r.Verdict.Note,
		VerdictAuth:           string(r.Verdict.Auth),
		VerdictAuthAtMs:       breakGlassMillis(r.Verdict.AuthAt),
		VerdictReviewedAtMs:   breakGlassMillis(r.Verdict.ReviewedAt),
		VerdictActAs:          actAs,
		Version:               1,
	}, nil
}

// breakGlassListQuery builds the list query of f: the scope, the state, the
// deadline, the cursor and the limit that f names, ordered by ID.
func breakGlassListQuery(f breakglass.Filter) (string, []any) {
	var (
		where []string
		args  []any
	)

	if f.Scope != "" {
		where = append(where, "use_scope = ?")
		args = append(args, f.Scope.String())
	}

	if f.State != "" {
		where = append(where, "state = ?")
		args = append(args, string(f.State))
	}

	if !f.DueBefore.IsZero() {
		// A review with no deadline holds the time 0, and a deadline filter
		// selects it for no time.
		where = append(where, "due_at_ms <> 0 AND due_at_ms <= ?")
		args = append(args, breakGlassMillis(f.DueBefore))
	}

	if f.After != "" {
		where = append(where, "id > ?")
		args = append(args, f.After)
	}

	query := "SELECT " + breakGlassColumns + " FROM break_glass_reviews"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	query += " ORDER BY id"

	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}

	return query, args
}

// breakGlassRow scans one row of the list query, in the order of
// breakGlassColumns.
func breakGlassRow(rows *sql.Rows) (db.BreakGlassReview, error) {
	var row db.BreakGlassReview

	err := rows.Scan(
		&row.ID, &row.UseKind, &row.UseRef, &row.UseScope, &row.UseObject, &row.UseSubject, &row.UsePeople,
		&row.UseReason, &row.UseAtMs, &row.UseUntilMs, &row.State, &row.CreatedAtMs, &row.DueAtMs, &row.Calendar,
		&row.CalendarFromNamespace, &row.CalendarFromGroup, &row.Step, &row.NextNoticeAtMs, &row.VerdictReviewer,
		&row.VerdictNote, &row.VerdictAuth, &row.VerdictAuthAtMs, &row.VerdictReviewedAtMs, &row.VerdictActAs,
		&row.Version,
	)
	if err != nil {
		return db.BreakGlassReview{}, fmt.Errorf("scan break-glass review: %w", err)
	}

	return row, nil
}

// breakGlassReviewOf builds the review of a stored row and checks it with the
// rules of a stored review. It returns an error for a row that fails, so a
// corrupt row is never served as a review.
func breakGlassReviewOf(row db.BreakGlassReview) (breakglass.Review, error) {
	// An error returns the zero review, so that no caller reads a review from
	// it.
	var none breakglass.Review

	people, err := breakGlassNames(row.UsePeople)
	if err != nil {
		return none, fmt.Errorf("people of the use: %w", err)
	}

	actAs, err := breakGlassNames(row.VerdictActAs)
	if err != nil {
		return none, fmt.Errorf("act-as list of the verdict: %w", err)
	}

	r := breakglass.Review{
		ID: row.ID,
		Use: breakglass.Use{
			Kind:    breakglass.Kind(row.UseKind),
			Ref:     row.UseRef,
			Scope:   namespace.Name(row.UseScope),
			Object:  row.UseObject,
			Subject: row.UseSubject,
			People:  people,
			Reason:  row.UseReason,
			At:      breakGlassTime(row.UseAtMs),
			Until:   breakGlassTime(row.UseUntilMs),
		},
		State:     breakglass.State(row.State),
		CreatedAt: breakGlassTime(row.CreatedAtMs),
		DueAt:     breakGlassTime(row.DueAtMs),
		Calendar:  calendar.ID(row.Calendar),
		CalendarFrom: calendar.Scope{
			Namespace: namespace.Name(row.CalendarFromNamespace),
			Group:     row.CalendarFromGroup,
		},
		Step:         int(row.Step),
		NextNoticeAt: breakGlassTime(row.NextNoticeAtMs),
		Verdict: breakglass.Verdict{
			Reviewer:   row.VerdictReviewer,
			Note:       row.VerdictNote,
			Auth:       breakglass.AuthKind(row.VerdictAuth),
			AuthAt:     breakGlassTime(row.VerdictAuthAtMs),
			ReviewedAt: breakGlassTime(row.VerdictReviewedAtMs),
			ActAs:      actAs,
		},
		Version: row.Version,
	}

	if err := checkBreakGlassReview(r); err != nil {
		return none, err
	}

	return r, nil
}

// checkBreakGlassReview returns ErrInvalid for a stored review that fails the
// rules that the store keeps: the use passes its checks, the ID has the prefix
// of a review, the scope of the calendar names a namespace or a group, and a
// review with a verdict holds a verdict that CheckVerdict accepts.
func checkBreakGlassReview(r breakglass.Review) error {
	if err := r.Use.Validate(); err != nil {
		return err
	}

	if !strings.HasPrefix(r.ID, breakglass.IDPrefix) || len(r.ID) == len(breakglass.IDPrefix) {
		return fmt.Errorf("%w: a review ID starts with %s", breakglass.ErrInvalid, breakglass.IDPrefix)
	}

	if err := r.CalendarFrom.Validate(); err != nil {
		return err
	}

	if r.Version < 1 {
		return fmt.Errorf("%w: a stored review holds a version of at least 1", breakglass.ErrInvalid)
	}

	switch r.State {
	case breakglass.StatePending:
		if !breakGlassVerdictEmpty(r.Verdict) {
			return fmt.Errorf("%w: a pending review holds no verdict", breakglass.ErrInvalid)
		}

		return nil
	case breakglass.StateAccepted, breakglass.StateDisputed:
		// The verdict of a stored review was checked against the review when
		// the store recorded it, when the review was pending.
		pending := r
		pending.State = breakglass.StatePending

		return breakglass.CheckVerdict(pending, r.State, r.Verdict)
	default:
		return fmt.Errorf("%w: unknown state %q", breakglass.ErrInvalid, r.State)
	}
}

// breakGlassVerdictEmpty reports whether v is the verdict of a pending review.
func breakGlassVerdictEmpty(v breakglass.Verdict) bool {
	return v.Reviewer == "" && v.Note == "" && v.Auth == "" && v.AuthAt.IsZero() && v.ReviewedAt.IsZero() &&
		len(v.ActAs) == 0
}

// breakGlassNames decodes the JSON array of names that a column holds.
func breakGlassNames(raw string) ([]string, error) {
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decode the list: %w", err)
	}

	return out, nil
}

// breakGlassTime returns the time of ms, or the zero time for 0: a review
// stores a missing time as 0, so that a query that filters or orders the
// reviews reads no time in the far past.
func breakGlassTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}

	return time.UnixMilli(ms).UTC()
}

// breakGlassMillis returns the Unix milliseconds of t, or 0 for the zero time.
func breakGlassMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return t.UnixMilli()
}

// appendBreakGlassEntry appends e to the audit chain of its scope in the
// transaction of q.
func appendBreakGlassEntry(ctx context.Context, q *db.Queries, e audit.Entry) error {
	if _, err := appendAuditEntry(ctx, q, e); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	return nil
}
