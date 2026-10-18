// Package quotatest is the conformance suite for quota.Counter adapters.
// Every adapter runs it:
//
//	func TestCounterConformance(t *testing.T) {
//		t.Parallel()
//		quotatest.Run(t, func(t *testing.T) quotatest.Harness { return newHarness(t) })
//	}
//
// The suite comes before the first adapter. A reference counter in memory,
// in the tests of this package, runs it.
package quotatest

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/quota"
)

// Harness is one fresh, empty database for one case.
type Harness struct {
	Counter quota.Counter
	Domain  Domain
}

// Domain makes the rows that the counter needs and that other ports own.
type Domain interface {
	// CreateNamespace creates the namespace name and returns its fence.
	CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error)
	// BumpFence increments the failover version of the namespace of fence,
	// as a failover does, and returns the new fence.
	BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error)
}

// Factory returns a fresh harness for one case.
type Factory func(t *testing.T) Harness

// The namespace and the time of the cases.
const ns = namespace.Name("payments")

// t0 is the time of the cases.
func t0() time.Time {
	return time.Unix(1_790_000_000, 0).UTC()
}

// Run executes the suite against harnesses from newHarness.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h Harness, fence namespace.Fence)
	}{
		{"a reservation below the limit counts as reserved", testReserve},
		{"a reservation at the limit is ErrExceeded and stores nothing", testReserveExceeded},
		{"a reservation passes every check or stores nothing", testReserveAllOrNothing},
		{"a reservation is idempotent by its ID", testReserveIdempotent},
		{"a known ID with other keys is ErrConflict", testReserveConflict},
		{"two reservations at the same time never both take the last unit", testReserveRace},
		{"settle moves a unit from reserved to settled", testSettle},
		{"release ends a reservation with no unit", testRelease},
		{"an unknown reservation is ErrNotFound", testUnknown},
		{"a reservation after its end time counts no more", testExpired},
		{"requests count in their window only", testRecord},
		{"set settled writes the count of the inventory", testSetSettled},
		{"a stale fence writes nothing", testFenced},
		{"a reservation that is not valid is ErrInvalid", testInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)

			fence, err := h.Domain.CreateNamespace(t.Context(), ns)
			if err != nil {
				t.Fatalf("CreateNamespace() = %v", err)
			}

			tc.run(t, h, fence)
		})
	}
}

func key(scope string) quota.Key {
	return quota.Key{Namespace: ns, Blueprint: "postgres", Kind: quota.KindUnits, Scope: scope}
}

func requestKey() quota.Key {
	return quota.Key{Namespace: ns, Blueprint: "postgres", Kind: quota.KindRequests, Scope: "requester:alice"}
}

func check(k quota.Key, maxUnits int) quota.Check {
	return quota.Check{Key: k, Window: 0, Max: maxUnits}
}

func reservation(id quota.ReservationID, keys ...quota.Key) quota.Reservation {
	return quota.Reservation{
		ID: id, Namespace: ns, State: quota.ReservationOpen, Keys: keys, At: t0(), EndsAt: t0().Add(24 * time.Hour),
	}
}

func usage(t *testing.T, h Harness, at time.Time, checks ...quota.Check) []quota.Usage {
	t.Helper()

	got, err := h.Counter.Usage(t.Context(), checks, at)
	if err != nil {
		t.Fatalf("Usage() = %v", err)
	}

	if len(got) != len(checks) {
		t.Fatalf("Usage() gave %d usages for %d checks", len(got), len(checks))
	}

	return got
}

func wantUsage(t *testing.T, h Harness, k quota.Key, settled, reserved int) {
	t.Helper()

	u := usage(t, h, t0(), check(k, 0))[0]
	if u.Key != k || u.Settled != settled || u.Reserved != reserved {
		t.Fatalf("Usage(%s) = %+v, want %d settled and %d reserved", k, u, settled, reserved)
	}
}

func mustReserve(t *testing.T, h Harness, fence namespace.Fence, r quota.Reservation, checks ...quota.Check) {
	t.Helper()

	if _, err := h.Counter.Reserve(t.Context(), r, checks, fence); err != nil {
		t.Fatalf("Reserve(%s) = %v", r.ID, err)
	}
}

func testReserve(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	k := key("namespace")

	got, err := h.Counter.Reserve(t.Context(), reservation("req-1/1", k), []quota.Check{check(k, 2)}, fence)
	if err != nil {
		t.Fatalf("Reserve() = %v", err)
	}

	if got.ID != "req-1/1" || got.State != quota.ReservationOpen {
		t.Fatalf("Reserve() = %+v, want req-1/1 open", got)
	}

	wantUsage(t, h, k, 0, 1)
}

func testReserveExceeded(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	k := key("namespace")
	mustReserve(t, h, fence, reservation("req-1/1", k), check(k, 1))

	_, err := h.Counter.Reserve(t.Context(), reservation("req-2/1", k), []quota.Check{check(k, 1)}, fence)

	var ee *quota.ExceededError
	if !errors.Is(err, quota.ErrExceeded) || !errors.As(err, &ee) {
		t.Fatalf("Reserve() = %v, want an *ExceededError", err)
	}

	if want := []quota.Exceeded{{Key: k, Used: 1, Max: 1}}; !reflect.DeepEqual(ee.Exceeded, want) {
		t.Fatalf("Exceeded = %+v, want %+v", ee.Exceeded, want)
	}

	wantUsage(t, h, k, 0, 1)
}

func testReserveAllOrNothing(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	all, mine := key("namespace"), key("requester:alice")
	mustReserve(t, h, fence, reservation("req-1/1", mine), check(mine, 1))

	_, err := h.Counter.Reserve(t.Context(), reservation("req-2/1", all, mine),
		[]quota.Check{check(all, 10), check(mine, 1)}, fence)
	if !errors.Is(err, quota.ErrExceeded) {
		t.Fatalf("Reserve() = %v, want ErrExceeded", err)
	}

	wantUsage(t, h, all, 0, 0)
	wantUsage(t, h, mine, 0, 1)
}

func testReserveIdempotent(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	k := key("namespace")
	mustReserve(t, h, fence, reservation("req-1/1", k), check(k, 1))
	// The retry passes although the limit is full: the reservation is the
	// one that the counter already holds.
	mustReserve(t, h, fence, reservation("req-1/1", k), check(k, 1))
	wantUsage(t, h, k, 0, 1)
}

func testReserveConflict(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	k, other := key("namespace"), key("requester:alice")
	mustReserve(t, h, fence, reservation("req-1/1", k), check(k, 5))

	_, err := h.Counter.Reserve(t.Context(), reservation("req-1/1", other), []quota.Check{check(other, 5)}, fence)
	if !errors.Is(err, quota.ErrConflict) {
		t.Fatalf("Reserve(other keys) = %v, want ErrConflict", err)
	}
}

func testReserveRace(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	const tries = 8

	k := key("namespace")
	errs := make([]error, tries)

	var wg sync.WaitGroup

	for i := range tries {
		wg.Go(func() {
			id := quota.ReservationID("req-" + strconv.Itoa(i) + "/1")
			_, errs[i] = h.Counter.Reserve(t.Context(), reservation(id, k), []quota.Check{check(k, 1)}, fence)
		})
	}

	wg.Wait()

	passed := 0

	for _, err := range errs {
		switch {
		case err == nil:
			passed++
		case !errors.Is(err, quota.ErrExceeded):
			t.Fatalf("Reserve() = %v, want nil or ErrExceeded", err)
		default:
		}
	}

	if passed != 1 {
		t.Fatalf("%d of %d reservations passed a limit of 1", passed, tries)
	}

	wantUsage(t, h, k, 0, 1)
}

func testSettle(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	k := key("namespace")
	mustReserve(t, h, fence, reservation("req-1/1", k), check(k, 1))

	for range 2 {
		if err := h.Counter.Settle(t.Context(), "req-1/1", fence); err != nil {
			t.Fatalf("Settle() = %v", err)
		}
	}

	wantUsage(t, h, k, 1, 0)

	if err := h.Counter.Release(t.Context(), "req-1/1", fence); !errors.Is(err, quota.ErrEnded) {
		t.Fatalf("Release(settled) = %v, want ErrEnded", err)
	}
}

func testRelease(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	k := key("namespace")
	mustReserve(t, h, fence, reservation("req-1/1", k), check(k, 1))

	for range 2 {
		if err := h.Counter.Release(t.Context(), "req-1/1", fence); err != nil {
			t.Fatalf("Release() = %v", err)
		}
	}

	wantUsage(t, h, k, 0, 0)

	if err := h.Counter.Settle(t.Context(), "req-1/1", fence); !errors.Is(err, quota.ErrEnded) {
		t.Fatalf("Settle(released) = %v, want ErrEnded", err)
	}

	// The slot is free again.
	mustReserve(t, h, fence, reservation("req-2/1", k), check(k, 1))
}

func testUnknown(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	if err := h.Counter.Settle(t.Context(), "req-0/1", fence); !errors.Is(err, quota.ErrNotFound) {
		t.Fatalf("Settle(unknown) = %v, want ErrNotFound", err)
	}

	if err := h.Counter.Release(t.Context(), "req-0/1", fence); !errors.Is(err, quota.ErrNotFound) {
		t.Fatalf("Release(unknown) = %v, want ErrNotFound", err)
	}
}

func testExpired(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	k := key("namespace")
	r := reservation("req-1/1", k)
	mustReserve(t, h, fence, r, check(k, 1))

	if u := usage(t, h, r.EndsAt, check(k, 1))[0]; u.Reserved != 0 {
		t.Fatalf("Usage(at the end time) = %+v, want no reservation", u)
	}

	if u := usage(t, h, r.EndsAt.Add(-time.Second), check(k, 1))[0]; u.Reserved != 1 {
		t.Fatalf("Usage(before the end time) = %+v, want 1 reservation", u)
	}
}

func testRecord(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	k := requestKey()
	c := quota.Check{Key: k, Window: time.Hour, Max: 2}

	cs := []quota.Check{c}

	for i := range 2 {
		err := h.Counter.Record(t.Context(), k, t0().Add(time.Duration(i)*time.Minute), cs, fence)
		if err != nil {
			t.Fatalf("Record(%d) = %v", i, err)
		}
	}

	err := h.Counter.Record(t.Context(), k, t0().Add(30*time.Minute), cs, fence)
	if !errors.Is(err, quota.ErrExceeded) {
		t.Fatalf("Record(third in the window) = %v, want ErrExceeded", err)
	}

	if u := usage(t, h, t0().Add(30*time.Minute), c)[0]; u.Requests != 2 {
		t.Fatalf("Usage() = %+v, want 2 requests", u)
	}

	// The first request leaves the window after one hour.
	if err := h.Counter.Record(t.Context(), k, t0().Add(time.Hour), []quota.Check{c}, fence); err != nil {
		t.Fatalf("Record(after the window of the first) = %v", err)
	}
}

func testSetSettled(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	k := key("namespace")
	if err := h.Counter.SetSettled(t.Context(), k, 7, fence); err != nil {
		t.Fatalf("SetSettled() = %v", err)
	}

	mustReserve(t, h, fence, reservation("req-1/1", k), check(k, 8))
	wantUsage(t, h, k, 7, 1)

	if err := h.Counter.SetSettled(t.Context(), k, -1, fence); !errors.Is(err, quota.ErrInvalid) {
		t.Fatalf("SetSettled(-1) = %v, want ErrInvalid", err)
	}

	if err := h.Counter.SetSettled(t.Context(), requestKey(), 1, fence); !errors.Is(err, quota.ErrInvalid) {
		t.Fatalf("SetSettled(requests key) = %v, want ErrInvalid", err)
	}
}

func testFenced(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	k := key("namespace")

	if _, err := h.Domain.BumpFence(t.Context(), fence); err != nil {
		t.Fatalf("BumpFence() = %v", err)
	}

	_, err := h.Counter.Reserve(t.Context(), reservation("req-1/1", k), []quota.Check{check(k, 1)}, fence)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Reserve(stale fence) = %v, want ErrFenced", err)
	}

	if err := h.Counter.SetSettled(t.Context(), k, 1, fence); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("SetSettled(stale fence) = %v, want ErrFenced", err)
	}

	wantUsage(t, h, k, 0, 0)
}

func testInvalid(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	r := reservation("req-1/1")
	if _, err := h.Counter.Reserve(t.Context(), r, nil, fence); !errors.Is(err, quota.ErrInvalid) {
		t.Fatalf("Reserve(no key) = %v, want ErrInvalid", err)
	}
}
