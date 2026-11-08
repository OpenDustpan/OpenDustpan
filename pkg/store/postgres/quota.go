package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// quotaRequestIDPrefix starts the ID of a recorded request. The ID is no
// domain ID: the counter uses it to tell two requests of one key at the same
// time apart.
const quotaRequestIDPrefix = "qr-"

// QuotaCounter returns the counter of quota usage. Each write checks the
// installation state and the fence of its namespace in its own transaction.
func (s *Store) QuotaCounter() QuotaCounter {
	return QuotaCounter{s: s}
}

// QuotaCounter is quota.Counter over the database of a Store.
type QuotaCounter struct {
	s *Store
}

var _ quota.Counter = QuotaCounter{s: nil}

// InTx implements quota.Counter. fn runs with a quota.Tx in one write
// transaction that holds the fence, so the revision of a submit and its
// reservation commit together, or neither does.
//
// A PostgreSQL write transaction runs at READ COMMITTED. The counter
// serializes the writes of one key with a lock on the row of the key (see
// lockQuotaKeys): a transaction that waits for the lock reads the usage of
// the transaction that held it, so two writes never both pass a limit.
func (c QuotaCounter) InTx(ctx context.Context, fence namespace.Fence, fn func(tx quota.Tx) error) error {
	return c.s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		return fn(&quotaTx{q: q, fence: fence})
	})
}

// Settle implements quota.Counter.
func (c QuotaCounter) Settle(ctx context.Context, id quota.ReservationID, fence namespace.Fence) error {
	return c.end(ctx, id, fence, quota.ReservationSettled)
}

// Release implements quota.Counter.
func (c QuotaCounter) Release(ctx context.Context, id quota.ReservationID, fence namespace.Fence) error {
	return c.end(ctx, id, fence, quota.ReservationReleased)
}

// Usage implements quota.Counter. Every read of the checks sees one snapshot
// of the database.
func (c QuotaCounter) Usage(ctx context.Context, checks []quota.Check, when time.Time) ([]quota.Usage, error) {
	var out []quota.Usage

	err := c.s.inSnapshotTx(ctx, func(q *db.Queries) error {
		var readErr error

		out, readErr = quotaUsage(ctx, q, checks, when)

		return readErr
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// SetSettled implements quota.Counter. It writes the count that the reconcile
// of the inventory counts, also when the counter knows no key yet.
func (c QuotaCounter) SetSettled(ctx context.Context, key quota.Key, count int, fence namespace.Fence) error {
	if key.Kind != quota.KindUnits || count < 0 {
		return fmt.Errorf("%w: settled count %d of key %s", quota.ErrInvalid, count, key)
	}

	if key.Namespace != fence.Namespace {
		return fmt.Errorf("%w: key names %q, fence names %q", namespace.ErrFenced, key.Namespace, fence.Namespace)
	}

	return c.s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		err := q.SetQuotaSettled(ctx, db.SetQuotaSettledParams{
			Namespace:    key.Namespace.String(),
			Blueprint:    key.Blueprint,
			Kind:         string(key.Kind),
			Scope:        key.Scope,
			SettledCount: int64(count),
		})
		if err != nil {
			return fmt.Errorf("write settled count of key %s: %w", key, err)
		}

		return nil
	})
}

// end ends the reservation id with the state to, in a transaction of its own.
func (c QuotaCounter) end(
	ctx context.Context, id quota.ReservationID, fence namespace.Fence, to quota.ReservationState,
) error {
	return c.s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		return endQuotaReservation(ctx, q, fence.Namespace, id, to)
	})
}

// quotaTx is quota.Tx over the queries of one write transaction of a Store.
// The transaction holds the fence and every write of fn, so a limit check and
// the write that passes it commit together.
type quotaTx struct {
	q     *db.Queries
	fence namespace.Fence
}

// Reserve implements quota.Tx. A reservation that the counter knows returns
// the stored one, so a retry of a submit stores nothing twice.
func (t *quotaTx) Reserve(
	ctx context.Context, r quota.Reservation, checks []quota.Check,
) (quota.Reservation, error) {
	if err := r.Validate(); err != nil {
		return quota.Reservation{}, err
	}

	// Defense in depth: the fence check of the transaction compares the
	// failover version of the namespace of the fence. This check stays so
	// that a reservation of one namespace cannot be written under the fence
	// of another namespace, whatever the caller passes.
	if r.Namespace != t.fence.Namespace {
		return quota.Reservation{}, fmt.Errorf(
			"%w: reservation %s names %q, fence names %q",
			namespace.ErrFenced, r.ID, r.Namespace, t.fence.Namespace,
		)
	}

	if err := checkQuotaFenceKeys(t.fence, checks); err != nil {
		return quota.Reservation{}, err
	}

	have, err := getQuotaReservation(ctx, t.q, r.Namespace, r.ID)
	switch {
	case err == nil:
		// The counter knows the ID: the retry returns the stored reservation,
		// and the same ID with other keys is a conflict.
		if !sameQuotaKeys(have.Keys, r.Keys) {
			return quota.Reservation{}, fmt.Errorf("%w: reservation %s", quota.ErrConflict, r.ID)
		}

		return have, nil
	case errors.Is(err, quota.ErrNotFound):
		// The counter does not know the ID: the checks below decide.
	default:
		return quota.Reservation{}, err
	}

	err = lockQuotaKeys(ctx, t.q, checks)
	if err != nil {
		return quota.Reservation{}, err
	}

	us, err := quotaUsage(ctx, t.q, checks, r.At)
	if err != nil {
		return quota.Reservation{}, err
	}

	if x := quota.Evaluate(checks, us, 1); x != nil {
		return quota.Reservation{}, &quota.ExceededError{Exceeded: x}
	}

	if err := insertQuotaReservation(ctx, t.q, r); err != nil {
		return quota.Reservation{}, err
	}

	return r, nil
}

// Record implements quota.Tx. One request is stored for the key of each check
// only when each check passes, so a submit that a units limit refused records
// no request.
func (t *quotaTx) Record(ctx context.Context, at time.Time, checks []quota.Check) error {
	if err := checkQuotaRecordChecks(t.fence, checks); err != nil {
		return err
	}

	if err := lockQuotaKeys(ctx, t.q, checks); err != nil {
		return err
	}

	us, err := quotaUsage(ctx, t.q, checks, at)
	if err != nil {
		return err
	}

	if x := quota.Evaluate(checks, us, 1); x != nil {
		return &quota.ExceededError{Exceeded: x}
	}

	for _, c := range checks {
		err = t.q.InsertQuotaRequest(ctx, db.InsertQuotaRequestParams{
			ID:        ident.NewID(quotaRequestIDPrefix),
			Namespace: c.Key.Namespace.String(),
			Blueprint: c.Key.Blueprint,
			Kind:      string(c.Key.Kind),
			Scope:     c.Key.Scope,
			AtMs:      codec.Millis(at),
		})
		if err != nil {
			return fmt.Errorf("insert quota request of key %s: %w", c.Key, err)
		}
	}

	return nil
}

// SetEnd implements quota.Tx.
func (t *quotaTx) SetEnd(ctx context.Context, id quota.ReservationID, endsAt time.Time) error {
	r, err := getQuotaReservation(ctx, t.q, t.fence.Namespace, id)
	if err != nil {
		return err
	}

	if r.State != quota.ReservationOpen {
		return fmt.Errorf("%w: reservation %s is %s", quota.ErrEnded, id, r.State)
	}

	if !endsAt.After(r.At) {
		return fmt.Errorf("%w: reservation %s cannot end at %v, before its time %v",
			quota.ErrInvalid, id, endsAt, r.At)
	}

	n, err := t.q.SetQuotaReservationEnd(ctx, db.SetQuotaReservationEndParams{
		EndsAtMs:  codec.Millis(endsAt),
		Namespace: t.fence.Namespace.String(),
		ID:        string(id),
	})
	if err != nil {
		return fmt.Errorf("set end time of quota reservation %s: %w", id, err)
	}

	// Defense in depth: the read above returns ErrEnded for a reservation
	// that ended, and the update itself names the open state. This check
	// stays so that a lost update cannot pass as a change.
	if n == 0 {
		return fmt.Errorf("%w: reservation %s changed", quota.ErrConflict, id)
	}

	return nil
}

// lockQuotaKeys makes the row of the key of each check, when it has none, and
// locks it FOR UPDATE until the end of the transaction. The rows are locked in
// the order of their keys, so two transactions that share two keys cannot
// deadlock.
//
// The lock is what serializes the writes of one key. A PostgreSQL write
// transaction runs at READ COMMITTED, so without it two transactions can read
// the same usage, both pass quota.Evaluate and both write, and a limit is
// exceeded. A transaction that waits for the lock reads the usage of the
// transaction that held it.
func lockQuotaKeys(ctx context.Context, q *db.Queries, checks []quota.Check) error {
	keys := make([]quota.Key, 0, len(checks))
	for _, c := range checks {
		keys = append(keys, c.Key)
	}

	slices.SortFunc(keys, compareQuotaKeys)

	for _, k := range keys {
		err := q.EnsureQuotaKey(ctx, db.EnsureQuotaKeyParams{
			Namespace: k.Namespace.String(),
			Blueprint: k.Blueprint,
			Kind:      string(k.Kind),
			Scope:     k.Scope,
		})
		if err != nil {
			return fmt.Errorf("make row of quota key %s: %w", k, err)
		}

		_, err = q.LockQuotaKey(ctx, db.LockQuotaKeyParams{
			Namespace: k.Namespace.String(),
			Blueprint: k.Blueprint,
			Kind:      string(k.Kind),
			Scope:     k.Scope,
		})
		if err != nil {
			return fmt.Errorf("lock quota key %s: %w", k, err)
		}
	}

	return nil
}

// insertQuotaReservation stores the reservation r with its keys.
func insertQuotaReservation(ctx context.Context, q *db.Queries, r quota.Reservation) error {
	err := q.InsertQuotaReservation(ctx, db.InsertQuotaReservationParams{
		Namespace: r.Namespace.String(),
		ID:        string(r.ID),
		State:     string(r.State),
		AtMs:      codec.Millis(r.At),
		EndsAtMs:  codec.Millis(r.EndsAt),
	})
	if err != nil {
		// A reservation of this ID is in the table, and the read of the
		// caller saw no row, so another transaction wrote it between the read
		// and this insert: report the lost write rather than a driver error.
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: reservation %s exists", quota.ErrConflict, r.ID)
		}

		return fmt.Errorf("insert quota reservation %s: %w", r.ID, err)
	}

	for _, k := range r.Keys {
		err = q.InsertQuotaReservationKey(ctx, db.InsertQuotaReservationKeyParams{
			Namespace:     r.Namespace.String(),
			ReservationID: string(r.ID),
			Blueprint:     k.Blueprint,
			Kind:          string(k.Kind),
			Scope:         k.Scope,
		})
		if err != nil {
			return fmt.Errorf("insert key %s of quota reservation %s: %w", k, r.ID, err)
		}
	}

	return nil
}

// endQuotaReservation ends the reservation id with the state to in the
// transaction of q. A settle moves one unit of each key of the reservation
// from reserved to settled, and a release moves none. The read holds the row
// of the reservation FOR UPDATE, so two ends of one reservation wait for each
// other, and the second reads the state that the first wrote.
func endQuotaReservation(
	ctx context.Context, q *db.Queries, ns namespace.Name, id quota.ReservationID, to quota.ReservationState,
) error {
	r, err := getQuotaReservationForUpdate(ctx, q, ns, id)
	if err != nil {
		return err
	}

	if r.State == to {
		// The end already ran: a retry changes nothing.
		return nil
	}

	if r.State != quota.ReservationOpen {
		return fmt.Errorf("%w: reservation %s is %s", quota.ErrEnded, id, r.State)
	}

	n, err := q.SetQuotaReservationState(ctx, db.SetQuotaReservationStateParams{
		State:     string(to),
		Namespace: ns.String(),
		ID:        string(id),
	})
	if err != nil {
		return fmt.Errorf("end quota reservation %s: %w", id, err)
	}

	// Defense in depth: the read above and this update run in one
	// transaction, and the read holds the row FOR UPDATE, so no other write
	// changes the row between them. This check stays so that a changed update
	// cannot end a reservation that a writer already ended, and cannot count
	// its units twice.
	if n == 0 {
		return fmt.Errorf("%w: reservation %s changed", quota.ErrConflict, id)
	}

	if to != quota.ReservationSettled {
		return nil
	}

	return addQuotaSettled(ctx, q, ns, r.Keys)
}

// addQuotaSettled adds one settled unit for each key of a settled reservation.
func addQuotaSettled(ctx context.Context, q *db.Queries, ns namespace.Name, keys []quota.Key) error {
	for _, k := range keys {
		err := q.AddQuotaSettled(ctx, db.AddQuotaSettledParams{
			Namespace: ns.String(),
			Blueprint: k.Blueprint,
			Kind:      string(k.Kind),
			Scope:     k.Scope,
		})
		if err != nil {
			return fmt.Errorf("add settled count of key %s: %w", k, err)
		}
	}

	return nil
}

// getQuotaReservation returns the stored reservation id of ns with its keys.
// It returns quota.ErrNotFound for an ID that the counter does not know, and
// quota.ErrInvalid for a row whose state is not one of the three states.
func getQuotaReservation(
	ctx context.Context, q *db.Queries, ns namespace.Name, id quota.ReservationID,
) (quota.Reservation, error) {
	row, err := q.GetQuotaReservation(ctx, db.GetQuotaReservationParams{Namespace: ns.String(), ID: string(id)})
	if err != nil {
		return quota.Reservation{}, quotaReservationError(id, "select quota reservation", err)
	}

	return quotaReservationFrom(ctx, q, ns, id, quotaReservationRow{
		state: row.State, atMs: row.AtMs, endsAtMs: row.EndsAtMs,
	})
}

// getQuotaReservationForUpdate returns the stored reservation id of ns with
// its keys, and holds its row FOR UPDATE until the end of the transaction. It
// returns quota.ErrNotFound for an ID that the counter does not know, and
// quota.ErrInvalid for a row whose state is not one of the three states.
func getQuotaReservationForUpdate(
	ctx context.Context, q *db.Queries, ns namespace.Name, id quota.ReservationID,
) (quota.Reservation, error) {
	row, err := q.GetQuotaReservationForUpdate(ctx, db.GetQuotaReservationForUpdateParams{
		Namespace: ns.String(), ID: string(id),
	})
	if err != nil {
		return quota.Reservation{}, quotaReservationError(id, "select quota reservation for update", err)
	}

	return quotaReservationFrom(ctx, q, ns, id, quotaReservationRow{
		state: row.State, atMs: row.AtMs, endsAtMs: row.EndsAtMs,
	})
}

// quotaReservationError translates the error of a reservation read: no rows
// becomes quota.ErrNotFound, and any other error is wrapped with what and the
// ID.
func quotaReservationError(id quota.ReservationID, what string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: reservation %s", quota.ErrNotFound, id)
	}

	return fmt.Errorf("%s %s: %w", what, id, err)
}

// quotaReservationRow is the state and the times of a reservation, from the
// plain read or the locked read.
type quotaReservationRow struct {
	state    string
	atMs     int64
	endsAtMs int64
}

// quotaReservationFrom builds the reservation id of ns from the state and the
// times of its row, and reads its keys.
func quotaReservationFrom(
	ctx context.Context, q *db.Queries, ns namespace.Name, id quota.ReservationID, row quotaReservationRow,
) (quota.Reservation, error) {
	parsed, err := quotaReservationState(row.state)
	if err != nil {
		return quota.Reservation{}, fmt.Errorf("reservation %s: %w", id, err)
	}

	keys, err := listQuotaReservationKeys(ctx, q, ns, id)
	if err != nil {
		return quota.Reservation{}, err
	}

	return quota.Reservation{
		ID:        id,
		Namespace: ns,
		State:     parsed,
		Keys:      keys,
		At:        codec.FromMillis(row.atMs),
		EndsAt:    codec.FromMillis(row.endsAtMs),
	}, nil
}

// listQuotaReservationKeys returns the keys of the reservation id, in the
// order of the table.
func listQuotaReservationKeys(
	ctx context.Context, q *db.Queries, ns namespace.Name, id quota.ReservationID,
) ([]quota.Key, error) {
	rows, err := q.ListQuotaReservationKeys(ctx, db.ListQuotaReservationKeysParams{
		Namespace: ns.String(), ReservationID: string(id),
	})
	if err != nil {
		return nil, fmt.Errorf("select keys of quota reservation %s: %w", id, err)
	}

	keys := make([]quota.Key, 0, len(rows))

	for _, row := range rows {
		keys = append(keys, quota.Key{
			Namespace: ns,
			Blueprint: row.Blueprint,
			Kind:      quota.Kind(row.Kind),
			Scope:     row.Scope,
		})
	}

	return keys, nil
}

// quotaReservationState converts a stored state into a reservation state. A
// stored value outside the three states is a row that the counter never wrote.
func quotaReservationState(state string) (quota.ReservationState, error) {
	switch quota.ReservationState(state) {
	case quota.ReservationOpen, quota.ReservationSettled, quota.ReservationReleased:
		return quota.ReservationState(state), nil
	default:
		return "", fmt.Errorf("%w: stored reservation state %q", quota.ErrInvalid, state)
	}
}

// sameQuotaKeys reports whether a and b hold the same keys, in any order. The
// stored keys come back in the order of the table, not in the order of the
// caller that reserved them.
func sameQuotaKeys(a, b []quota.Key) bool {
	if len(a) != len(b) {
		return false
	}

	left, right := slices.Clone(a), slices.Clone(b)
	slices.SortFunc(left, compareQuotaKeys)
	slices.SortFunc(right, compareQuotaKeys)

	return slices.Equal(left, right)
}

// compareQuotaKeys orders two keys of one namespace by their parts.
func compareQuotaKeys(a, b quota.Key) int {
	return strings.Compare(a.String(), b.String())
}

// checkQuotaFenceKeys checks that the key of each check counts in the
// namespace of fence. It returns an error that wraps namespace.ErrFenced for a
// key of another namespace.
//
// Defense in depth: the queries of a write name the namespace of the fence.
// This check stays so that a check of another namespace cannot read the usage
// of that namespace under the fence of this one.
func checkQuotaFenceKeys(fence namespace.Fence, checks []quota.Check) error {
	for _, c := range checks {
		if c.Key.Namespace != fence.Namespace {
			return fmt.Errorf("%w: key %s names %q, fence names %q",
				namespace.ErrFenced, c.Key, c.Key.Namespace, fence.Namespace)
		}
	}

	return nil
}

// checkQuotaRecordChecks checks the checks of a record: each key counts
// requests, and each key counts in the namespace of fence.
func checkQuotaRecordChecks(fence namespace.Fence, checks []quota.Check) error {
	for _, c := range checks {
		if c.Key.Kind != quota.KindRequests {
			return fmt.Errorf("%w: record of key %s", quota.ErrInvalid, c.Key)
		}
	}

	return checkQuotaFenceKeys(fence, checks)
}

// quotaUsage returns the usage of the key of each of checks at the time when,
// in the order of checks, in the transaction of q.
func quotaUsage(ctx context.Context, q *db.Queries, checks []quota.Check, when time.Time) ([]quota.Usage, error) {
	out := make([]quota.Usage, 0, len(checks))

	for _, c := range checks {
		u, err := quotaUsageOf(ctx, q, c, when)
		if err != nil {
			return nil, err
		}

		out = append(out, u)
	}

	return out, nil
}

// quotaUsageOf returns the usage of the key of one check at the time when: the
// settled count and the open reservations of a key of KindUnits, and the
// requests in the window of a key of KindRequests. A key that the counter does
// not know uses nothing.
func quotaUsageOf(ctx context.Context, q *db.Queries, c quota.Check, when time.Time) (quota.Usage, error) {
	ns := c.Key.Namespace.String()

	if c.Key.Kind == quota.KindUnits {
		settled, err := q.GetQuotaSettled(ctx, db.GetQuotaSettledParams{
			Namespace: ns, Blueprint: c.Key.Blueprint, Kind: string(c.Key.Kind), Scope: c.Key.Scope,
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return quota.Usage{}, fmt.Errorf("select settled count of key %s: %w", c.Key, err)
		}

		if errors.Is(err, pgx.ErrNoRows) {
			// The counter knows no settled count of the key yet.
			settled = 0
		}

		reserved, err := q.CountQuotaReserved(ctx, db.CountQuotaReservedParams{
			Namespace: ns, Blueprint: c.Key.Blueprint, Kind: string(c.Key.Kind), Scope: c.Key.Scope,
			AtMs: codec.Millis(when),
		})
		if err != nil {
			return quota.Usage{}, fmt.Errorf("count reservations of key %s: %w", c.Key, err)
		}

		return quota.Usage{Key: c.Key, Settled: int(settled), Reserved: int(reserved), Requests: 0}, nil
	}

	if c.Key.Kind != quota.KindRequests {
		// A rule of KindExpr counts through its expression, and a value
		// outside the kinds is a caller error.
		return quota.Usage{}, fmt.Errorf("%w: the counter does not count key %s", quota.ErrInvalid, c.Key)
	}

	requests, err := q.CountQuotaRequests(ctx, db.CountQuotaRequestsParams{
		Namespace: ns, Blueprint: c.Key.Blueprint, Kind: string(c.Key.Kind), Scope: c.Key.Scope,
		AfterMs: codec.Millis(when.Add(-c.Window)), AtMs: codec.Millis(when),
	})
	if err != nil {
		return quota.Usage{}, fmt.Errorf("count requests of key %s: %w", c.Key, err)
	}

	return quota.Usage{Key: c.Key, Settled: 0, Reserved: 0, Requests: int(requests)}, nil
}
