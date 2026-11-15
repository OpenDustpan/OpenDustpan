package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/internal/quota/quotatest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// quotaDomain makes the namespace rows that the counter needs.
type quotaDomain struct {
	t *testing.T
	s *sqlite.Store
}

func (d quotaDomain) CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error) {
	d.t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                name,
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := d.s.CreateNamespace(ctx, ns); err != nil {
		return namespace.Fence{}, fmt.Errorf("create namespace %s: %w", name, err)
	}

	return ns.Fence(), nil
}

func (d quotaDomain) BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error) {
	d.t.Helper()

	if _, err := d.s.BumpFailoverVersions(ctx); err != nil {
		return namespace.Fence{}, fmt.Errorf("bump failover versions: %w", err)
	}

	ns, err := d.s.GetNamespace(ctx, fence.Namespace)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("get namespace: %w", err)
	}

	return ns.Fence(), nil
}

func newQuotaHarness(t *testing.T) quotatest.Harness {
	t.Helper()

	s := openTemplated(t)

	return quotatest.Harness{Counter: s.QuotaCounter(), Domain: quotaDomain{t: t, s: s}}
}

// TestQuotaCounterConformance runs the quota suite against the SQLite counter.
func TestQuotaCounterConformance(t *testing.T) {
	t.Parallel()

	quotatest.Run(t, newQuotaHarness)
}

// quotaNS is the namespace of the concurrency cases.
const quotaNS = namespace.Name("payments")

func quotaKey(scope string) quota.Key {
	return quota.Key{Namespace: quotaNS, Blueprint: "postgres", Kind: quota.KindUnits, Scope: scope}
}

func quotaCheck(k quota.Key, maxUnits int) quota.Check {
	return quota.Check{Key: k, Window: 0, Max: maxUnits}
}

func quotaReservation(id quota.ReservationID, keys ...quota.Key) quota.Reservation {
	return quota.Reservation{
		ID: id, Namespace: quotaNS, State: quota.ReservationOpen, Keys: keys,
		At: time.Unix(1_790_000_000, 0).UTC(), EndsAt: time.Unix(1_790_000_000, 0).UTC().Add(24 * time.Hour),
	}
}

// quotaReserve reserves r in one transaction of its own.
func quotaReserve(
	t *testing.T, c quota.Counter, fence namespace.Fence, r quota.Reservation, checks ...quota.Check,
) error {
	t.Helper()

	return c.InTx(t.Context(), fence, func(tx quota.Tx) error {
		_, err := tx.Reserve(t.Context(), r, checks)

		return err
	})
}

// TestQuotaCounterConcurrentLimit proves that eight reserves that run at the
// same time on one key never pass a limit of 1 more than once: keys are locked
// in one order and the whole transaction is tried again on a busy database.
func TestQuotaCounterConcurrentLimit(t *testing.T) {
	t.Parallel()

	d := quotaDomain{t: t, s: openTemplated(t)}
	c := d.s.QuotaCounter()

	fence, err := d.CreateNamespace(t.Context(), quotaNS)
	if err != nil {
		t.Fatalf("CreateNamespace() = %v", err)
	}

	k := quotaKey("namespace")

	const tries = 8

	errs := make([]error, tries)

	var wg sync.WaitGroup

	for i := range tries {
		wg.Go(func() {
			id := quota.ReservationID(fmt.Sprintf("req-%d/1", i))
			errs[i] = quotaReserve(t, c, fence, quotaReservation(id, k), quotaCheck(k, 1))
		})
	}

	wg.Wait()

	passed := 0

	for _, err := range errs {
		switch {
		case err == nil:
			passed++
		case errors.Is(err, quota.ErrExceeded):
		default:
			t.Fatalf("Reserve() = %v, want nil or quota.ErrExceeded", err)
		}
	}

	if passed != 1 {
		t.Fatalf("%d of %d reservations passed a limit of 1, want 1", passed, tries)
	}

	u, err := c.Usage(t.Context(), []quota.Check{quotaCheck(k, 0)}, time.Unix(1_790_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("Usage() = %v", err)
	}

	if len(u) != 1 || u[0].Reserved != 1 {
		t.Fatalf("Usage() = %+v, want 1 reserved", u)
	}
}

// TestQuotaCounterConcurrentKeys proves that two reserves of the same two keys
// in opposite order do not deadlock: both pass within the limit.
func TestQuotaCounterConcurrentKeys(t *testing.T) {
	t.Parallel()

	d := quotaDomain{t: t, s: openTemplated(t)}
	c := d.s.QuotaCounter()

	fence, err := d.CreateNamespace(t.Context(), quotaNS)
	if err != nil {
		t.Fatalf("CreateNamespace() = %v", err)
	}

	one, two := quotaKey("requester:alice"), quotaKey("group:dba")

	errs := make([]error, 2)

	var wg sync.WaitGroup

	wg.Go(func() {
		errs[0] = quotaReserve(t, c, fence, quotaReservation("req-a/1", one, two),
			quotaCheck(one, 5), quotaCheck(two, 5))
	})
	wg.Go(func() {
		errs[1] = quotaReserve(t, c, fence, quotaReservation("req-b/1", two, one),
			quotaCheck(two, 5), quotaCheck(one, 5))
	})
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("reserve %d = %v, want both within the limit", i, err)
		}
	}

	usages, err := c.Usage(t.Context(),
		[]quota.Check{quotaCheck(one, 0), quotaCheck(two, 0)}, time.Unix(1_790_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("Usage() = %v", err)
	}

	if len(usages) != 2 || usages[0].Reserved != 2 || usages[1].Reserved != 2 {
		t.Fatalf("Usage() = %+v, want 2 reservations of each key", usages)
	}
}

// TestQuotaCounterConcurrentKeysRefused proves that two reserves of the same
// two keys in opposite order both fail above the limit of one key, and that
// neither stores its other key: the write is all or nothing.
func TestQuotaCounterConcurrentKeysRefused(t *testing.T) {
	t.Parallel()

	d := quotaDomain{t: t, s: openTemplated(t)}
	c := d.s.QuotaCounter()

	fence, err := d.CreateNamespace(t.Context(), quotaNS)
	if err != nil {
		t.Fatalf("CreateNamespace() = %v", err)
	}

	three, four := quotaKey("requester:bob"), quotaKey("group:sre")

	// One settled unit already uses key four, so the limit of 1 leaves no room
	// for a reservation.
	err = c.SetSettled(t.Context(), four, 1, fence)
	if err != nil {
		t.Fatalf("SetSettled() = %v", err)
	}

	over := make([]error, 2)

	var wg sync.WaitGroup

	wg.Go(func() {
		over[0] = quotaReserve(t, c, fence, quotaReservation("req-c/1", three, four),
			quotaCheck(three, 10), quotaCheck(four, 1))
	})
	wg.Go(func() {
		over[1] = quotaReserve(t, c, fence, quotaReservation("req-d/1", four, three),
			quotaCheck(four, 1), quotaCheck(three, 10))
	})
	wg.Wait()

	for i, err := range over {
		if !errors.Is(err, quota.ErrExceeded) {
			t.Fatalf("reserve %d above the limit = %v, want quota.ErrExceeded", i, err)
		}
	}

	usages, err := c.Usage(t.Context(),
		[]quota.Check{quotaCheck(three, 0), quotaCheck(four, 0)}, time.Unix(1_790_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("Usage() = %v", err)
	}

	if len(usages) != 2 || usages[0].Reserved != 0 || usages[0].Settled != 0 {
		t.Fatalf("Usage() = %+v, want no reservation and no settled unit of the other key", usages)
	}

	if len(usages) != 2 || usages[1].Reserved != 0 || usages[1].Settled != 1 {
		t.Fatalf("Usage() = %+v, want the settled unit of key four alone", usages)
	}
}
