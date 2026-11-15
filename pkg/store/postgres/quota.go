package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// QuotaCounter returns the quota counter of the store. A reservation and the
// guarded write that holds it commit in one transaction: the caller of
// QuotaCounter.InTx reserves inside the transaction that also stores its own
// rows.
func (s *Store) QuotaCounter() *QuotaCounter {
	return &QuotaCounter{s: s}
}

// QuotaCounter is quota.Counter over the database of a Store.
type QuotaCounter struct {
	s *Store
}

var _ quota.Counter = (*QuotaCounter)(nil)

// InTx implements quota.Counter.
func (c *QuotaCounter) InTx(ctx context.Context, fence namespace.Fence, fn func(tx quota.Tx) error) error {
	return c.retryQuota(ctx, fence, func(q *db.Queries) error {
		return fn(&quotaTx{q: q, fence: fence})
	})
}

// Settle implements quota.Counter.
func (c *QuotaCounter) Settle(ctx context.Context, id quota.ReservationID, fence namespace.Fence) error {
	return c.end(ctx, id, fence, quota.ReservationSettled)
}

// Release implements quota.Counter.
func (c *QuotaCounter) Release(ctx context.Context, id quota.ReservationID, fence namespace.Fence) error {
	return c.end(ctx, id, fence, quota.ReservationReleased)
}

// SetSettled implements quota.Counter.
func (c *QuotaCounter) SetSettled(ctx context.Context, key quota.Key, count int, fence namespace.Fence) error {
	if key.Kind != quota.KindUnits || count < 0 {
		return fmt.Errorf("%w: settled count %d of %s", quota.ErrInvalid, count, key)
	}

	// Defense in depth: the caller passes one fence for one namespace, and a
	// key of another namespace would write outside the fence that InTx
	// checked. This check stays so that a change of the query cannot skip it.
	if key.Namespace != fence.Namespace {
		return fmt.Errorf("%w: key of %s under the fence of %s", namespace.ErrFenced, key.Namespace, fence.Namespace)
	}

	return c.retryQuota(ctx, fence, func(q *db.Queries) error {
		nsID, err := quotaNamespaceID(ctx, q, fence.Namespace)
		if err != nil {
			return err
		}

		err = quotaLockKey(ctx, q, nsID, key)
		if err != nil {
			return err
		}

		_, err = q.SetQuotaSettled(ctx, db.SetQuotaSettledParams{
			Settled: int64(count), NamespaceID: nsID, BlueprintID: key.Blueprint, Kind: string(key.Kind),
			Scope: key.Scope,
		})
		if err != nil {
			return fmt.Errorf("set quota settled: %w", err)
		}

		return nil
	})
}

// Usage implements quota.Counter.
func (c *QuotaCounter) Usage(ctx context.Context, checks []quota.Check, when time.Time) ([]quota.Usage, error) {
	out := make([]quota.Usage, 0, len(checks))

	err := c.s.inSnapshotTx(ctx, func(q *db.Queries) error {
		ids := make(map[namespace.Name]string, 1)

		for _, ch := range checks {
			u, err := quotaUsageFor(ctx, q, ids, ch, when)
			if err != nil {
				return err
			}

			out = append(out, u)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// retryQuota runs fn in one fenced transaction and tries the whole
// transaction once more when a database conflict ends it: a serialization
// failure or a deadlock. Only a limit failure (quota.ErrExceeded) passes
// through without a new try.
func (c *QuotaCounter) retryQuota(ctx context.Context, fence namespace.Fence, fn func(q *db.Queries) error) error {
	for attempt := range 2 {
		err := readConflict(c.s.inFencedTx(ctx, fence, fn))
		if err == nil || attempt == 1 || !errors.Is(err, namespace.ErrUnavailable) {
			return err
		}
	}

	return nil
}

// end ends reservation id the way to. A settle adds one unit to the settled
// count of each of its keys, in the transaction of the state change; a
// release changes no count.
func (c *QuotaCounter) end(
	ctx context.Context, id quota.ReservationID, fence namespace.Fence, to quota.ReservationState,
) error {
	return c.retryQuota(ctx, fence, func(q *db.Queries) error {
		nsID, err := quotaNamespaceID(ctx, q, fence.Namespace)
		if err != nil {
			return err
		}

		moved, err := quotaEndReservation(ctx, q, nsID, id, to)
		if err != nil || !moved || to != quota.ReservationSettled {
			return err
		}

		return quotaAddSettled(ctx, q, nsID, id)
	})
}

// quotaEndReservation moves reservation id to the state to and reports
// whether the row moved now. A reservation that is already in to changes
// nothing; one that ended in another state is quota.ErrEnded.
func quotaEndReservation(
	ctx context.Context, q *db.Queries, nsID string, id quota.ReservationID, to quota.ReservationState,
) (bool, error) {
	row, err := q.GetQuotaReservation(ctx, db.GetQuotaReservationParams{NamespaceID: nsID, ID: string(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("%w: %s", quota.ErrNotFound, id)
	}

	if err != nil {
		return false, fmt.Errorf("read quota reservation: %w", err)
	}

	state := quota.ReservationState(row.State)

	if state == to {
		return false, nil // Idempotent: the same end changes nothing.
	}

	if state != quota.ReservationOpen {
		return false, fmt.Errorf("%w: %s is %s", quota.ErrEnded, id, row.State)
	}

	n, err := q.SetQuotaReservationState(ctx, db.SetQuotaReservationStateParams{
		State: string(to), NamespaceID: nsID, ID: string(id), FromState: string(quota.ReservationOpen),
	})
	if err != nil {
		return false, fmt.Errorf("end quota reservation: %w", err)
	}

	if n == 0 {
		return false, fmt.Errorf("%w: %s ended during the write", quota.ErrEnded, id)
	}

	return true, nil
}

// quotaAddSettled adds one settled unit to each key of the reservation.
func quotaAddSettled(ctx context.Context, q *db.Queries, nsID string, id quota.ReservationID) error {
	keys, err := q.ListQuotaReservationKeys(ctx, db.ListQuotaReservationKeysParams{
		NamespaceID: nsID, ReservationID: string(id),
	})
	if err != nil {
		return fmt.Errorf("list quota reservation keys: %w", err)
	}

	for _, k := range keys {
		_, err = q.AddQuotaSettled(ctx, db.AddQuotaSettledParams{
			NamespaceID: nsID, BlueprintID: k.BlueprintID, Kind: k.Kind, Scope: k.Scope,
		})
		if err != nil {
			return fmt.Errorf("add quota settled: %w", err)
		}
	}

	return nil
}

// quotaTx is the counter inside one open transaction of the store. It checks
// the fence again before each write, so a transaction that a caller opens
// before a failover cannot write after it.
type quotaTx struct {
	q     *db.Queries
	fence namespace.Fence
}

var _ quota.Tx = (*quotaTx)(nil)

// Reserve implements quota.Tx.
func (tx *quotaTx) Reserve(
	ctx context.Context, r quota.Reservation, checks []quota.Check,
) (quota.Reservation, error) {
	if err := tx.checkFence(ctx); err != nil {
		return quota.Reservation{}, err
	}

	if err := quotaValidateReservation(tx.fence, r); err != nil {
		return quota.Reservation{}, err
	}

	nsID, err := quotaNamespaceID(ctx, tx.q, tx.fence.Namespace)
	if err != nil {
		return quota.Reservation{}, err
	}

	stored, found, err := quotaFindReservation(ctx, tx.q, nsID, r)
	if err != nil {
		return quota.Reservation{}, err
	}

	if found {
		return stored, nil
	}

	// Lock every key of the reservation and of the checks in one order, so
	// two reserves never deadlock and no limit passes twice.
	if err := tx.lockKeys(ctx, nsID, quotaLockKeys(r, checks)); err != nil {
		return quota.Reservation{}, err
	}

	if err := quotaCheckLimits(ctx, tx.q, nsID, checks, r.At); err != nil {
		return quota.Reservation{}, err
	}

	if err := tx.insertReservation(ctx, nsID, r); err != nil {
		return quota.Reservation{}, err
	}

	return r, nil
}

// Record implements quota.Tx.
func (tx *quotaTx) Record(ctx context.Context, submitted time.Time, checks []quota.Check) error {
	if err := tx.checkFence(ctx); err != nil {
		return err
	}

	unique, err := quotaRecordKeys(tx.fence, checks)
	if err != nil {
		return err
	}

	ids, err := tx.lockRecordKeys(ctx, unique)
	if err != nil {
		return err
	}

	byKey, err := quotaPrunedUsage(ctx, tx.q, ids, unique, submitted)
	if err != nil {
		return err
	}

	usages := make([]quota.Usage, 0, len(checks))
	for _, c := range checks {
		usages = append(usages, byKey[c.Key])
	}

	if x := quota.Evaluate(checks, usages, 1); x != nil {
		return &quota.ExceededError{Exceeded: x}
	}

	for _, c := range unique {
		if err := insertQuotaRequest(ctx, tx.q, ids[c.Key.Namespace], c, submitted); err != nil {
			return err
		}
	}

	return nil
}

// SetEnd implements quota.Tx.
func (tx *quotaTx) SetEnd(ctx context.Context, id quota.ReservationID, endsAt time.Time) error {
	if err := tx.checkFence(ctx); err != nil {
		return err
	}

	nsID, err := quotaNamespaceID(ctx, tx.q, tx.fence.Namespace)
	if err != nil {
		return err
	}

	row, err := tx.q.GetQuotaReservation(ctx, db.GetQuotaReservationParams{NamespaceID: nsID, ID: string(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s", quota.ErrNotFound, id)
	}

	if err != nil {
		return fmt.Errorf("read quota reservation: %w", err)
	}

	if quota.ReservationState(row.State) != quota.ReservationOpen {
		return fmt.Errorf("%w: %s is %s", quota.ErrEnded, id, row.State)
	}

	at := codec.FromMillis(row.AtMs)
	if !endsAt.After(at) {
		return fmt.Errorf("%w: %s cannot end at %v, before its time %v", quota.ErrInvalid, id, endsAt, at)
	}

	n, err := tx.q.SetQuotaReservationEnd(ctx, db.SetQuotaReservationEndParams{
		EndsAtMs: codec.Millis(endsAt), NamespaceID: nsID, ID: string(id),
	})
	if err != nil {
		return fmt.Errorf("set quota reservation end: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("%w: %s ended during the write", quota.ErrEnded, id)
	}

	return nil
}

// checkFence returns namespace.ErrFenced when the failover version of the
// namespace of the transaction moved, and namespace.ErrNotFound when the
// namespace is gone.
//
// Defense in depth: QuotaCounter.InTx checks the fence before it opens the
// transaction. This check stays so that a caller that holds a transaction
// across a failover cannot write.
func (tx *quotaTx) checkFence(ctx context.Context) error {
	version, err := tx.q.GetFailoverVersionForShare(ctx, tx.fence.Namespace.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return namespace.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("read failover version: %w", err)
	}

	if version != tx.fence.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}

// lockKeys makes the row of each key exist and locks it for the rest of the
// transaction, in the order that quotaLockKeys gives, so two writes on one key
// never both read the count and then write.
func (tx *quotaTx) lockKeys(ctx context.Context, nsID string, keys []quota.Key) error {
	for _, k := range keys {
		if err := quotaLockKey(ctx, tx.q, nsID, k); err != nil {
			return err
		}
	}

	return nil
}

// lockRecordKeys locks the key of each check and returns the namespace ID of
// each namespace of the checks.
func (tx *quotaTx) lockRecordKeys(ctx context.Context, checks []quota.Check) (map[namespace.Name]string, error) {
	ids := make(map[namespace.Name]string, 1)

	for _, c := range checks {
		nsID, err := quotaNamespaceID(ctx, tx.q, c.Key.Namespace)
		if err != nil {
			return nil, err
		}

		ids[c.Key.Namespace] = nsID

		if err := quotaLockKey(ctx, tx.q, nsID, c.Key); err != nil {
			return nil, err
		}
	}

	return ids, nil
}

// insertReservation writes the reservation row and one row for each of its
// keys, in the order that quotaSortKeys gives.
func (tx *quotaTx) insertReservation(ctx context.Context, nsID string, r quota.Reservation) error {
	err := tx.q.InsertQuotaReservation(ctx, db.InsertQuotaReservationParams{
		NamespaceID: nsID,
		ID:          string(r.ID),
		State:       string(quota.ReservationOpen),
		AtMs:        codec.Millis(r.At),
		EndsAtMs:    codec.Millis(r.EndsAt),
	})
	if err != nil {
		return fmt.Errorf("insert quota reservation: %w", err)
	}

	for _, k := range quotaSortKeys(r.Keys) {
		err = tx.q.InsertQuotaReservationKey(ctx, db.InsertQuotaReservationKeyParams{
			NamespaceID: nsID, ReservationID: string(r.ID), BlueprintID: k.Blueprint, Kind: string(k.Kind),
			Scope: k.Scope,
		})
		if err != nil {
			return fmt.Errorf("insert quota reservation key: %w", err)
		}
	}

	return nil
}

// quotaNamespaceID returns the stable ID of the namespace that has the name,
// and namespace.ErrNotFound when no namespace has it.
func quotaNamespaceID(ctx context.Context, q *db.Queries, ns namespace.Name) (string, error) {
	id, err := q.GetNamespaceID(ctx, ns.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return "", namespace.ErrNotFound
	}

	if err != nil {
		return "", fmt.Errorf("read namespace id: %w", err)
	}

	return id, nil
}

// quotaValidateReservation checks the shape of a reservation and that it
// belongs to the fenced namespace.
func quotaValidateReservation(fence namespace.Fence, r quota.Reservation) error {
	if err := r.Validate(); err != nil {
		return err
	}

	// Defense in depth: InTx checked the fence of its namespace. This check
	// stays so that a reservation of another namespace cannot write under it.
	if r.Namespace != fence.Namespace {
		return fmt.Errorf(
			"%w: reservation of %s under the fence of %s",
			namespace.ErrFenced,
			r.Namespace,
			fence.Namespace,
		)
	}

	return nil
}

// quotaFindReservation returns the stored reservation of r.ID with found
// true when the counter holds one. It returns quota.ErrConflict when the
// stored one names other keys.
func quotaFindReservation(
	ctx context.Context, q *db.Queries, nsID string, r quota.Reservation,
) (quota.Reservation, bool, error) {
	// none is the reservation of an ID that the counter does not hold.
	var none quota.Reservation

	row, err := q.GetQuotaReservation(ctx, db.GetQuotaReservationParams{NamespaceID: nsID, ID: string(r.ID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("read quota reservation: %w", err)
	}

	keys, err := q.ListQuotaReservationKeys(ctx, db.ListQuotaReservationKeysParams{
		NamespaceID: nsID, ReservationID: string(r.ID),
	})
	if err != nil {
		return none, false, fmt.Errorf("list quota reservation keys: %w", err)
	}

	stored := quota.Reservation{
		ID:        quota.ReservationID(row.ID),
		Namespace: r.Namespace,
		State:     quota.ReservationState(row.State),
		Keys:      quotaKeysOf(r.Namespace, keys),
		At:        codec.FromMillis(row.AtMs),
		EndsAt:    codec.FromMillis(row.EndsAtMs),
	}

	if !slices.Equal(quotaSortKeys(stored.Keys), quotaSortKeys(r.Keys)) {
		return none, false, fmt.Errorf("%w: %s", quota.ErrConflict, r.ID)
	}

	return stored, true, nil
}

// quotaRecordKeys checks that each check is a request of the fenced namespace
// and returns the checks with no repeated key, so a repeated key counts once
// and gets one stored row.
func quotaRecordKeys(fence namespace.Fence, checks []quota.Check) ([]quota.Check, error) {
	unique := make([]quota.Check, 0, len(checks))
	seen := make(map[quota.Key]bool, len(checks))

	for _, c := range checks {
		if c.Key.Kind != quota.KindRequests {
			return nil, fmt.Errorf("%w: record of %s", quota.ErrInvalid, c.Key)
		}

		// Defense in depth: InTx checked the fence of its namespace. This
		// check stays so that a request of another namespace cannot write.
		if c.Key.Namespace != fence.Namespace {
			return nil, fmt.Errorf("%w: request of %s under the fence of %s",
				namespace.ErrFenced, c.Key.Namespace, fence.Namespace)
		}

		if seen[c.Key] {
			continue
		}

		seen[c.Key] = true
		unique = append(unique, c)
	}

	return unique, nil
}

// quotaCheckLimits reads the usage of each check at the time at and returns
// the limit failure of the first check that runs over its max.
func quotaCheckLimits(ctx context.Context, q *db.Queries, nsID string, checks []quota.Check, at time.Time) error {
	usages := make([]quota.Usage, 0, len(checks))

	for _, c := range checks {
		u, err := quotaReadUsage(ctx, q, nsID, c, at)
		if err != nil {
			return err
		}

		usages = append(usages, u)
	}

	if x := quota.Evaluate(checks, usages, 1); x != nil {
		return &quota.ExceededError{Exceeded: x}
	}

	return nil
}

// quotaPrunedUsage deletes the submits of each key that its window leaves out
// and returns the usage of the key of each check.
func quotaPrunedUsage(
	ctx context.Context, q *db.Queries, ids map[namespace.Name]string, checks []quota.Check, submitted time.Time,
) (map[quota.Key]quota.Usage, error) {
	byKey := make(map[quota.Key]quota.Usage, len(checks))

	for _, c := range checks {
		nsID := ids[c.Key.Namespace]

		_, err := q.DeleteQuotaRequestsBefore(ctx, db.DeleteQuotaRequestsBeforeParams{
			NamespaceID: nsID, BlueprintID: c.Key.Blueprint, Kind: string(c.Key.Kind), Scope: c.Key.Scope,
			BeforeMs: codec.Millis(submitted.Add(-c.Window)),
		})
		if err != nil {
			return nil, fmt.Errorf("prune quota requests: %w", err)
		}

		u, err := quotaReadUsage(ctx, q, nsID, c, submitted)
		if err != nil {
			return nil, err
		}

		byKey[c.Key] = u
	}

	return byKey, nil
}

// insertQuotaRequest stores one submit of one key.
func insertQuotaRequest(ctx context.Context, q *db.Queries, nsID string, c quota.Check, submitted time.Time) error {
	err := q.InsertQuotaRequest(ctx, db.InsertQuotaRequestParams{
		NamespaceID: nsID, BlueprintID: c.Key.Blueprint, Kind: string(c.Key.Kind), Scope: c.Key.Scope,
		SubmittedAtMs: codec.Millis(submitted),
	})
	if err != nil {
		return fmt.Errorf("insert quota request: %w", err)
	}

	return nil
}

// quotaKeysOf returns the keys of the stored rows of a reservation in ns.
func quotaKeysOf(ns namespace.Name, rows []db.ListQuotaReservationKeysRow) []quota.Key {
	out := make([]quota.Key, 0, len(rows))
	for _, row := range rows {
		out = append(out, quota.Key{
			Namespace: ns, Blueprint: row.BlueprintID, Kind: quota.Kind(row.Kind), Scope: row.Scope,
		})
	}

	return out
}

// quotaUsageFor returns the usage of one check at the time when, with the
// namespace lookups shared between the checks of one call. A key of a
// namespace that the store does not know uses nothing.
func quotaUsageFor(
	ctx context.Context, q *db.Queries, ids map[namespace.Name]string, c quota.Check, when time.Time,
) (quota.Usage, error) {
	nsID, known := ids[c.Key.Namespace]

	if !known {
		var err error

		nsID, err = quotaNamespaceID(ctx, q, c.Key.Namespace)
		if err != nil && !errors.Is(err, namespace.ErrNotFound) {
			return quota.Usage{}, err
		}

		if errors.Is(err, namespace.ErrNotFound) {
			nsID = ""
		}

		ids[c.Key.Namespace] = nsID
	}

	if nsID == "" {
		return quota.Usage{Key: c.Key, Settled: 0, Reserved: 0, Requests: 0}, nil
	}

	return quotaReadUsage(ctx, q, nsID, c, when)
}

// quotaReadUsage returns the usage of one check at the time when: the settled
// count and the open reservations whose end time is after when for a units
// key, and the submits inside the window for a requests key.
func quotaReadUsage(
	ctx context.Context, q *db.Queries, nsID string, c quota.Check, when time.Time,
) (quota.Usage, error) {
	u := quota.Usage{Key: c.Key, Settled: 0, Reserved: 0, Requests: 0}

	if c.Key.Kind == quota.KindUnits {
		settled, err := q.GetQuotaSettled(ctx, db.GetQuotaSettledParams{
			NamespaceID: nsID, BlueprintID: c.Key.Blueprint, Kind: string(c.Key.Kind), Scope: c.Key.Scope,
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return quota.Usage{}, fmt.Errorf("read quota settled: %w", err)
		}

		reserved, err := q.CountQuotaOpenReserved(ctx, db.CountQuotaOpenReservedParams{
			NamespaceID: nsID, BlueprintID: c.Key.Blueprint, Kind: string(c.Key.Kind), Scope: c.Key.Scope,
			AfterMs: codec.Millis(when),
		})
		if err != nil {
			return quota.Usage{}, fmt.Errorf("count quota reserved: %w", err)
		}

		u.Settled = int(settled)
		u.Reserved = int(reserved)

		return u, nil
	}

	// A requests key counts the submits inside its window. A key of any other
	// kind, an expression rule for example, counts nothing in the storage.
	if c.Key.Kind == quota.KindRequests {
		requests, err := q.CountQuotaRequests(ctx, db.CountQuotaRequestsParams{
			NamespaceID: nsID, BlueprintID: c.Key.Blueprint, Kind: string(c.Key.Kind), Scope: c.Key.Scope,
			AfterMs: codec.Millis(when.Add(-c.Window)), UpToMs: codec.Millis(when),
		})
		if err != nil {
			return quota.Usage{}, fmt.Errorf("count quota requests: %w", err)
		}

		u.Requests = int(requests)
	}

	return u, nil
}

// quotaLockKey makes the row of a key exist and takes its lock for the rest of
// the transaction, so two writes on one key never both read the count and then
// write.
func quotaLockKey(ctx context.Context, q *db.Queries, nsID string, k quota.Key) error {
	err := q.InsertQuotaKey(ctx, db.InsertQuotaKeyParams{
		NamespaceID: nsID, BlueprintID: k.Blueprint, Kind: string(k.Kind), Scope: k.Scope,
	})
	if err != nil {
		return fmt.Errorf("insert quota key: %w", err)
	}

	_, err = q.LockQuotaKey(ctx, db.LockQuotaKeyParams{
		NamespaceID: nsID, BlueprintID: k.Blueprint, Kind: string(k.Kind), Scope: k.Scope,
	})
	if err != nil {
		return fmt.Errorf("lock quota key: %w", err)
	}

	return nil
}

// quotaLockKeys returns the keys of the reservation and of the checks, with
// no duplicate, in the order that both Reserve adapters lock them.
func quotaLockKeys(r quota.Reservation, checks []quota.Check) []quota.Key {
	seen := make(map[quota.Key]bool, len(r.Keys)+len(checks))

	out := make([]quota.Key, 0, len(r.Keys)+len(checks))
	for _, k := range append(slices.Clone(r.Keys), quotaCheckKeys(checks)...) {
		if seen[k] {
			continue
		}

		seen[k] = true
		out = append(out, k)
	}

	return quotaSortKeys(out)
}

// quotaCheckKeys returns the key of each check.
func quotaCheckKeys(checks []quota.Check) []quota.Key {
	out := make([]quota.Key, 0, len(checks))
	for _, c := range checks {
		out = append(out, c.Key)
	}

	return out
}

// quotaSortKeys returns a copy of keys in the order that both dialects lock
// and compare them.
func quotaSortKeys(keys []quota.Key) []quota.Key {
	out := slices.Clone(keys)

	slices.SortFunc(out, func(a, b quota.Key) int {
		if diff := strings.Compare(a.Blueprint, b.Blueprint); diff != 0 {
			return diff
		}

		if diff := strings.Compare(string(a.Kind), string(b.Kind)); diff != 0 {
			return diff
		}

		return strings.Compare(a.Scope, b.Scope)
	})

	return out
}
