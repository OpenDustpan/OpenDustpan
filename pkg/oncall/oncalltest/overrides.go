package oncalltest

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/oncall"
)

// testCreateWithOverrides checks that a create cannot write overrides:
// only AddOverride writes them, with its own action, the time of the
// store and the cap.
func testCreateWithOverrides(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h, shop)

	s := sample("ocs-1", shop, "network", "alice")
	s.Overrides = []oncall.Override{override("ovr-1", "mallory")}

	if err := h.Schedules.Create(t.Context(), s, fence, entry(shop)); !errors.Is(err, oncall.ErrInvalid) {
		t.Fatalf("Create with an override: err = %v, want ErrInvalid", err)
	}

	_, err := h.Schedules.Get(t.Context(), oncall.Ref{Namespace: shop, Name: "network"})
	if !errors.Is(err, oncall.ErrNotFound) {
		t.Fatalf("Get after the refused create: err = %v, want ErrNotFound", err)
	}
}

// testUpdateKeepsOverrides checks that an update keeps the stored
// overrides: an update with no overrides does not delete the live ones,
// and an update with other overrides does not write them.
func testUpdateKeepsOverrides(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h, shop)
	create(t, h, fence, sample("ocs-1", shop, "network", "alice"))
	ref := oncall.Ref{Namespace: shop, Name: "network"}

	if err := h.Schedules.AddOverride(t.Context(), ref, override("ovr-bob", "bob"), fence, entry(shop)); err != nil {
		t.Fatalf("AddOverride: %v", err)
	}

	s := getNetwork(t, h)
	s.Layers[0].Members = []string{"alice", "carol"}
	s.Overrides = nil

	if err := h.Schedules.Update(t.Context(), s, fence, entry(shop)); err != nil {
		t.Fatalf("Update with no overrides: %v", err)
	}

	checkOnlyBob(t, h, "an update with no overrides")

	forged := override("ovr-forged", "mallory")
	forged.CreatedBy, forged.CreatedAt = "carol", now().AddDate(-1, 0, 0)
	forged.Start, forged.End = now().Add(-time.Hour), now().AddDate(1, 0, 0)

	s = getNetwork(t, h)
	s.Overrides = []oncall.Override{forged}

	if err := h.Schedules.Update(t.Context(), s, fence, entry(shop)); err != nil {
		t.Fatalf("Update with a forged override: %v", err)
	}

	checkOnlyBob(t, h, "an update with a forged override")

	if sh, err := getNetwork(t, h).At(now().Add(time.Minute)); err != nil || sh.Principal != "bob" {
		t.Fatalf("At = %s (%v), want bob of the stored override", sh.Principal, err)
	}
}

// checkOnlyBob checks that the schedule network holds only the override
// of bob, as AddOverride wrote it.
func checkOnlyBob(t *testing.T, h Harness, after string) {
	t.Helper()

	got := getNetwork(t, h)
	if len(got.Overrides) != 1 {
		t.Fatalf("after %s: %d overrides, want only the stored override of bob", after, len(got.Overrides))
	}

	if o := got.Overrides[0]; o.ID != "ovr-bob" || o.Principal != "bob" || o.CreatedBy != "bob" ||
		!o.CreatedAt.Equal(now()) {
		t.Fatalf("after %s: override %+v, want the stored override of bob", after, o)
	}
}

// testOverrideOrder checks that Get gives the overrides in the order of
// their adds, so the last add wins where two overrides cover one time. The
// IDs sort in the other order, and both adds have the same time of the
// store.
func testOverrideOrder(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h, shop)
	create(t, h, fence, sample("ocs-1", shop, "network", "alice"))
	ref := oncall.Ref{Namespace: shop, Name: "network"}

	for _, id := range []string{"ovr-z", "ovr-a"} {
		who := map[string]string{"ovr-z": "bob", "ovr-a": "carol"}[id]
		if err := h.Schedules.AddOverride(t.Context(), ref, override(id, who), fence, entry(shop)); err != nil {
			t.Fatalf("AddOverride %s: %v", id, err)
		}
	}

	got := getNetwork(t, h)
	if len(got.Overrides) != 2 || got.Overrides[0].ID != "ovr-z" || got.Overrides[1].ID != "ovr-a" {
		t.Fatalf("Get = %+v, want ovr-z then ovr-a, in the order of the adds", got.Overrides)
	}

	if sh, err := got.At(now().Add(time.Minute)); err != nil || sh.Principal != "carol" {
		t.Fatalf("At = %s (%v), want carol of the second add", sh.Principal, err)
	}
}

// testConcurrentOverrideOrder adds overrides at one time and checks that
// Get gives them in the order of the audit entries of their adds, which
// is the order of the transactions.
func testConcurrentOverrideOrder(t *testing.T, h Harness) {
	t.Helper()

	const n = 20

	fence := setup(t, h, shop)
	create(t, h, fence, sample("ocs-1", shop, "network", "alice"))
	ref := oncall.Ref{Namespace: shop, Name: "network"}

	var wg sync.WaitGroup

	errs := make([]error, n)

	for i := range n {
		wg.Go(func() {
			o := override(fmt.Sprintf("ovr-%02d", n-i), fmt.Sprintf("person-%d", i))
			e := entry(shop)
			e.ObjectID = o.ID
			errs[i] = h.Schedules.AddOverride(t.Context(), ref, o, fence, e)
		})
	}

	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		t.Fatalf("AddOverride: %v", err)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil || len(entries) != n+1 {
		t.Fatalf("audit entries = %d (%v), want the create and %d adds", len(entries), err, n)
	}

	got := getNetwork(t, h)
	if len(got.Overrides) != n {
		t.Fatalf("Get = %d overrides, want %d", len(got.Overrides), n)
	}

	for i := range got.Overrides {
		if want := entries[i+1].ObjectID; got.Overrides[i].ID != want {
			t.Fatalf("override %d = %s, want %s, in the order of the adds", i, got.Overrides[i].ID, want)
		}
	}

	last := got.Overrides[n-1]
	if sh, err := got.At(now().Add(time.Minute)); err != nil || sh.Principal != last.Principal {
		t.Fatalf("At = %s (%v), want %s of the last add", sh.Principal, err, last.Principal)
	}
}
