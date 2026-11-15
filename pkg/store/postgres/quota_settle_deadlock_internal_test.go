package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// The blueprint and the scopes of the settle deadlock case. The ICU
// collation of the scope column orders "group:a" before "group:B"; the byte
// order of the keys orders "group:B" first.
const (
	quotaSettleBlueprint = "postgres"
	quotaSettleScopeA    = "group:a"
	quotaSettleScopeB    = "group:B"
)

// quotaSettleLockSQL locks the row of the key scope FOR UPDATE in the
// transaction of the caller. The read returns pgx.ErrNoRows when the row
// does not exist.
const quotaSettleLockSQL = `SELECT settled_count FROM quota_keys
	WHERE namespace_id = $1 AND blueprint = $2 AND kind = $3 AND scope = $4
	FOR UPDATE`

// quotaSettleSettledSQL reads the settled count of the key scope.
const quotaSettleSettledSQL = `SELECT settled_count FROM quota_keys
	WHERE namespace_id = $1 AND blueprint = $2 AND kind = $3 AND scope = $4`

// quotaSettleWaitersSQL counts the sessions of this database that wait for a
// lock. The database of the store holds this test alone, so the count is one
// while the settle waits for the row of "group:B".
const quotaSettleWaitersSQL = `SELECT count(*)
	FROM pg_locks AS l
	JOIN pg_stat_activity AS a ON a.pid = l.pid
	WHERE NOT l.granted AND a.datname = current_database()`

// TestQuotaSettleDoesNotDeadlockWithACollationOrder proves that a settle of a
// reservation with two keys cannot deadlock with another transaction that
// locks the same two key rows in byte order, also when the collation of the
// scope column orders the scopes differently from byte order. The settle
// updates the key rows in quotaLockOrder, the byte order of the keys, so it
// waits for the first row while the other transaction takes the second one
// and commits, and no cycle forms. A settle that used the order of the table
// would take "group:a" first, and the other transaction would then deadlock
// on it.
func TestQuotaSettleDoesNotDeadlockWithACollationOrder(t *testing.T) {
	t.Parallel()

	s := openKeepLockStore(t, testPostgresURL(t))
	ctx := t.Context()

	setQuotaSettleCollation(ctx, t, s)

	ns, r := newQuotaSettleCase(ctx, t, s)

	rawTx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the raw transaction: %v", err)
	}

	t.Cleanup(func() {
		rollbackErr := rawTx.Rollback(context.WithoutCancel(ctx))
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Errorf("roll back the raw transaction: %v", rollbackErr)
		}
	})

	// The raw transaction holds the row of "group:B", the row that the
	// settle updates first in byte order.
	lockQuotaKeyRow(ctx, t, rawTx, ns, quotaSettleScopeB)

	done := make(chan error, 1)

	go func() { done <- s.QuotaCounter().Settle(ctx, r.ID, ns.Fence()) }()

	waitForQuotaLockWaiter(ctx, t, s, done)

	// The settle waits for "group:B". This transaction takes "group:a" and
	// commits, so the settle goes on. A cycle would end one of the two
	// transactions with a deadlock (SQLSTATE 40P01).
	lockQuotaKeyRow(ctx, t, rawTx, ns, quotaSettleScopeA)

	if err := rawTx.Commit(ctx); err != nil {
		t.Fatalf("commit the raw transaction: %v", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("settle: %v", err)
	}

	assertQuotaSettleSettled(ctx, t, s, ns)
}

// setQuotaSettleCollation changes the collation of the scope column of both
// quota tables to "en-US-x-icu", which orders "group:a" before "group:B". The
// baseline migration types the column as text.
func setQuotaSettleCollation(ctx context.Context, t *testing.T, s *Store) {
	t.Helper()

	for _, stmt := range []string{
		`ALTER TABLE quota_reservation_keys ALTER COLUMN scope TYPE text COLLATE "en-US-x-icu"`,
		`ALTER TABLE quota_keys ALTER COLUMN scope TYPE text COLLATE "en-US-x-icu"`,
	} {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("set the collation of a quota scope column: %v", err)
		}
	}
}

// newQuotaSettleCase creates the namespace of the case, reserves its two keys
// and returns the namespace and the reservation. Reserve makes the quota_keys
// row of each key: lockQuotaKeys calls EnsureQuotaKey before it locks the row.
// The case also proves that the reservation keys come back in the collation
// order of the table, "group:a" before "group:B", so a settle that used the
// table order would take "group:a" first.
func newQuotaSettleCase(ctx context.Context, t *testing.T, s *Store) (namespace.Namespace, quota.Reservation) {
	t.Helper()

	at := time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC)
	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: at,
		Name: "quota-settle", PrimaryInstallation: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	keyA := quota.Key{
		Namespace: ns.Name, Blueprint: quotaSettleBlueprint, Kind: quota.KindUnits, Scope: quotaSettleScopeA,
	}
	keyB := quota.Key{
		Namespace: ns.Name, Blueprint: quotaSettleBlueprint, Kind: quota.KindUnits, Scope: quotaSettleScopeB,
	}
	r := quota.Reservation{
		ID: "settle-deadlock", Namespace: ns.Name, State: quota.ReservationOpen,
		Keys: []quota.Key{keyA, keyB}, At: at, EndsAt: at.Add(24 * time.Hour),
	}
	checks := []quota.Check{
		{Key: keyA, Window: 0, Max: 1},
		{Key: keyB, Window: 0, Max: 1},
	}

	err := s.QuotaCounter().InTx(ctx, ns.Fence(), func(tx quota.Tx) error {
		_, reserveErr := tx.Reserve(ctx, r, checks)

		return reserveErr
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	assertQuotaSettleKeyOrder(ctx, t, s, ns, r.ID)

	return ns, r
}

// assertQuotaSettleKeyOrder fails the test unless the keys of the reservation
// id of ns come back in the collation order of the table, "group:a" before
// "group:B". The check keeps the case honest: the settle reads this order from
// the table, so a settle that used the table order would take "group:a" first.
func assertQuotaSettleKeyOrder(
	ctx context.Context, t *testing.T, s *Store, ns namespace.Namespace, id quota.ReservationID,
) {
	t.Helper()

	keys, err := listQuotaReservationKeys(ctx, s.queries, ns.Name, id)
	if err != nil {
		t.Fatalf("read the reservation keys: %v", err)
	}

	if len(keys) != 2 {
		t.Fatalf("reservation keys = %v, want 2 keys", keys)
	}

	if keys[0].Scope != quotaSettleScopeA || keys[1].Scope != quotaSettleScopeB {
		t.Fatalf("reservation keys = %v, want %s before %s", keys, quotaSettleScopeA, quotaSettleScopeB)
	}
}

// assertQuotaSettleSettled fails the test unless the settled count of each
// key of ns is one.
func assertQuotaSettleSettled(ctx context.Context, t *testing.T, s *Store, ns namespace.Namespace) {
	t.Helper()

	for _, scope := range []string{quotaSettleScopeA, quotaSettleScopeB} {
		var settled int64

		err := s.pool.QueryRow(
			ctx, quotaSettleSettledSQL, ns.ID.String(), quotaSettleBlueprint, string(quota.KindUnits), scope,
		).Scan(&settled)
		if err != nil {
			t.Fatalf("read the settled count of %s: %v", scope, err)
		}

		if settled != 1 {
			t.Fatalf("settled count of %s = %d, want 1", scope, settled)
		}
	}
}

// lockQuotaKeyRow locks the row of the key scope of ns FOR UPDATE in the
// transaction of tx, and fails the test when the row does not exist.
func lockQuotaKeyRow(ctx context.Context, t *testing.T, tx pgx.Tx, ns namespace.Namespace, scope string) {
	t.Helper()

	var settled int64

	err := tx.QueryRow(
		ctx, quotaSettleLockSQL, ns.ID.String(), quotaSettleBlueprint, string(quota.KindUnits), scope,
	).Scan(&settled)
	if err != nil {
		t.Fatalf("lock the row of %s: %v", scope, err)
	}
}

// waitForQuotaLockWaiter waits until a session of the database of s waits for
// a lock: the settle is then blocked on the row of "group:B". The loop runs
// while the context lives, and it fails when the settle ends first.
func waitForQuotaLockWaiter(ctx context.Context, t *testing.T, s *Store, done <-chan error) {
	t.Helper()

	for {
		var waiters int64

		if err := s.pool.QueryRow(ctx, quotaSettleWaitersSQL).Scan(&waiters); err != nil {
			t.Fatalf("read the lock waiters: %v", err)
		}

		if waiters > 0 {
			return
		}

		select {
		case err := <-done:
			t.Fatalf("the settle ended before it waited on the row of %s: %v", quotaSettleScopeB, err)
		default:
		}

		if ctx.Err() != nil {
			t.Fatal("no session of the database waits for a lock")
		}
	}
}
