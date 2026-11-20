package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
)

// The fixed values of the changed-row cases.
const (
	quotaConflictID      = quota.ReservationID("req-1/1")
	quotaConflictSettled = 3
)

// quotaConflictTime is the fixed time of the changed-row cases.
func quotaConflictTime() time.Time {
	return time.Unix(1_790_000_000, 0).UTC()
}

// quotaConflictKey is the key of the changed-row cases.
func quotaConflictKey() quota.Key {
	return quota.Key{Namespace: "payments", Blueprint: "postgres", Kind: quota.KindUnits, Scope: "namespace"}
}

// quotaConflictSeed is the stored state that a changed-row case keeps: the
// key, the time of the reservation and its end time.
type quotaConflictSeed struct {
	key    quota.Key
	at     time.Time
	endsAt time.Time
}

// quotaConflictFence makes the namespace payments in s and returns its fence.
func quotaConflictFence(t *testing.T, s *Store) namespace.Fence {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                "payments",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return ns.Fence()
}

// seedQuotaConflict stores a settled count of 3 and an open reservation of
// one key, and returns the stored state.
func seedQuotaConflict(t *testing.T, s *Store, fence namespace.Fence) quotaConflictSeed {
	t.Helper()

	seed := quotaConflictSeed{
		key:    quotaConflictKey(),
		at:     quotaConflictTime(),
		endsAt: quotaConflictTime().Add(24 * time.Hour),
	}

	if err := s.QuotaCounter().SetSettled(t.Context(), seed.key, quotaConflictSettled, fence); err != nil {
		t.Fatalf("SetSettled() = %v", err)
	}

	r := quota.Reservation{
		ID: quotaConflictID, Namespace: fence.Namespace, State: quota.ReservationOpen,
		Keys: []quota.Key{seed.key}, At: seed.at, EndsAt: seed.endsAt,
	}

	err := s.QuotaCounter().InTx(t.Context(), fence, func(tx quota.Tx) error {
		_, reserveErr := tx.Reserve(t.Context(), r, []quota.Check{{Key: seed.key, Window: 0, Max: 5}})

		return reserveErr
	})
	if err != nil {
		t.Fatalf("Reserve() = %v", err)
	}

	return seed
}

// quotaEndedBeforeUpdate is the executor of one transaction whose first
// update of a reservation runs a direct UPDATE through the same transaction
// that ends the reservation first. The method under test then reads the
// reservation as open and its own update matches no row: the row changed
// between the read and the write.
type quotaEndedBeforeUpdate struct {
	tx      pgx.Tx
	ns      namespace.Name
	id      quota.ReservationID
	changed bool
}

var _ db.DBTX = (*quotaEndedBeforeUpdate)(nil)

// quotaEndReservationSQL ends the open reservation id of the namespace name,
// as a writer that commits between the read and the update of the caller does.
const quotaEndReservationSQL = `UPDATE quota_reservations SET state = 'released'
WHERE namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = $1) AND id = $2 AND state = 'open'`

// Exec runs the direct UPDATE of the row before the update of the method
// under test, and the update of the method after it.
func (q *quotaEndedBeforeUpdate) Exec(
	ctx context.Context, query string, args ...any,
) (pgconn.CommandTag, error) {
	if !q.changed && strings.Contains(query, "UPDATE quota_reservations") {
		q.changed = true

		if err := q.end(ctx); err != nil {
			return pgconn.CommandTag{}, err
		}
	}

	tag, err := q.tx.Exec(ctx, query, args...)
	if err != nil {
		return pgconn.CommandTag{}, fmt.Errorf("run the update of the changed-row case: %w", err)
	}

	return tag, nil
}

// Query passes the read through to the transaction.
func (q *quotaEndedBeforeUpdate) Query(
	ctx context.Context, query string, args ...any,
) (pgx.Rows, error) {
	rows, err := q.tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read the row of the changed-row case: %w", err)
	}

	return rows, nil
}

// QueryRow passes the read of one row through to the transaction.
func (q *quotaEndedBeforeUpdate) QueryRow(
	ctx context.Context, query string, args ...any,
) pgx.Row {
	return q.tx.QueryRow(ctx, query, args...)
}

// end ends the reservation through the same transaction.
func (q *quotaEndedBeforeUpdate) end(ctx context.Context) error {
	tag, err := q.tx.Exec(ctx, quotaEndReservationSQL, q.ns.String(), string(q.id))
	if err != nil {
		return fmt.Errorf("end the reservation before the update: %w", err)
	}

	if n := tag.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: ended %d reservations, want 1", errQuotaConflictRowCount, n)
	}

	return nil
}

// checkQuotaChangedRowConflict runs one changed-row case: it stores a settled
// count and an open reservation, runs the method under test in a transaction
// whose direct UPDATE ends the reservation before the update of the method,
// and checks the refusal, the unchanged row and the unchanged settled count.
func checkQuotaChangedRowConflict(
	t *testing.T, run func(t *testing.T, q *db.Queries, fence namespace.Fence) error,
) {
	t.Helper()

	s := openKeepLockStore(t, testPostgresURL(t))
	fence := quotaConflictFence(t, s)
	seed := seedQuotaConflict(t, s, fence)

	if !quotaConflictCall(t, s, fence, run) {
		t.Fatal("the transaction ran no update of the reservation")
	}

	checkQuotaConflictState(t, s, fence, seed)
}

// quotaConflictCall runs the method under test in one transaction whose
// direct UPDATE ends the reservation before the update of the method. It
// returns whether the update of the method ran, and it rolls the transaction
// back, so every write of the call is undone.
func quotaConflictCall(
	t *testing.T, s *Store, fence namespace.Fence,
	run func(t *testing.T, q *db.Queries, fence namespace.Fence) error,
) bool {
	t.Helper()

	ctx := t.Context()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the transaction: %v", err)
	}

	defer func() {
		if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Errorf("roll back the transaction: %v", rollbackErr)
		}
	}()

	changed := &quotaEndedBeforeUpdate{tx: tx, ns: fence.Namespace, id: quotaConflictID, changed: false}

	if err := run(t, db.New(changed), fence); !errors.Is(err, quota.ErrConflict) {
		t.Fatalf("the call = %v, want ErrConflict", err)
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("roll back the transaction: %v", err)
	}

	return changed.changed
}

// checkQuotaConflictState checks that the stored reservation and the settled
// count are the seed, after the refused call rolled back.
func checkQuotaConflictState(t *testing.T, s *Store, fence namespace.Fence, seed quotaConflictSeed) {
	t.Helper()

	stored, err := getQuotaReservation(t.Context(), s.queries, fence.Namespace, quotaConflictID)
	if err != nil {
		t.Fatalf("read the stored reservation: %v", err)
	}

	if stored.State != quota.ReservationOpen || !stored.At.Equal(seed.at) || !stored.EndsAt.Equal(seed.endsAt) {
		t.Fatalf("the stored reservation = %+v, want open from %v until %v", stored, seed.at, seed.endsAt)
	}

	got, err := s.QuotaCounter().Usage(t.Context(), []quota.Check{{Key: seed.key, Window: 0, Max: 0}}, seed.at)
	if err != nil {
		t.Fatalf("Usage() = %v", err)
	}

	if len(got) != 1 || got[0].Settled != quotaConflictSettled || got[0].Reserved != 1 {
		t.Fatalf("the usage = %+v, want %d settled and 1 reserved", got, quotaConflictSettled)
	}
}

// TestQuotaSetEndChangedRowConflicts proves the changed-row check of SetEnd:
// a reservation that a writer ended between the read and the update of the
// end time gives ErrConflict, and the row and the settled count stay.
func TestQuotaSetEndChangedRowConflicts(t *testing.T) {
	t.Parallel()

	checkQuotaChangedRowConflict(t, func(t *testing.T, q *db.Queries, fence namespace.Fence) error {
		t.Helper()

		return (&quotaTx{q: q, fence: fence}).SetEnd(
			t.Context(),
			quotaConflictID,
			quotaConflictTime().Add(48*time.Hour),
		)
	})
}

// TestQuotaEndChangedRowConflicts proves the changed-row check of the end of
// a reservation: a reservation that a writer ended between the read and the
// update of the state gives ErrConflict, and the row and the settled count
// stay.
func TestQuotaEndChangedRowConflicts(t *testing.T) {
	t.Parallel()

	checkQuotaChangedRowConflict(t, func(t *testing.T, q *db.Queries, fence namespace.Fence) error {
		t.Helper()

		return endQuotaReservation(t.Context(), q, fence.Namespace, quotaConflictID, quota.ReservationSettled)
	})
}

// errQuotaConflictRowCount reports a direct UPDATE of a changed-row case that
// did not end one reservation.
var errQuotaConflictRowCount = errors.New("the direct update did not end one reservation")
