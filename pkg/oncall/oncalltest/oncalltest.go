// Package oncalltest is the conformance suite for oncall.Store and
// oncall.Handoffs implementations. Every store adapter runs it:
//
//	func TestOnCallConformance(t *testing.T) {
//		t.Parallel()
//		oncalltest.Run(t, func(t *testing.T) oncalltest.Harness { return newHarness(t) })
//	}
//
// A reference adapter in memory, in the tests of this package, runs the
// suite, so each case runs before the first SQL adapter.
package oncalltest

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/oncall"
)

// Harness is one fresh, empty database for one case.
type Harness struct {
	Schedules oncall.Store
	Handoffs  oncall.Handoffs
	Domain    Domain
}

// Domain makes the rows that a schedule needs and that other ports own,
// and reads the audit entries that the store appends.
type Domain interface {
	// CreateNamespace creates the namespace name and returns its fence.
	CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error)
	// BumpFence increments the failover version of the namespace of fence.
	BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error)
	// AuditEntries returns the audit entries of scope, in append order.
	AuditEntries(ctx context.Context, scope namespace.Name) ([]audit.Entry, error)
}

// Factory returns a fresh harness for one case.
type Factory func(t *testing.T) Harness

// Run executes the conformance suite against harnesses from newHarness.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h Harness)
	}{
		{"create stores a schedule and get returns it by name", testCreateGet},
		{"a name is unique in its namespace only", testNameUnique},
		{"update on an older version is ErrConflict", testUpdateConflict},
		{"two overrides at one time both stay", testOverrides},
		{"the copy of a local schedule is ErrInvalid", testCopyLocal},
		{"the copy of an external schedule replaces the shifts", testCopyExternal},
		{"for a principal, schedules of every namespace", testForPrincipal},
		{"delete makes the name free", testDelete},
		{"a stale fence changes nothing", testStaleFence},
		{"handoff notes list newest first", testHandoffs},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

const (
	shop     = namespace.Name("shop")
	platform = namespace.Name("platform")
	action   = "namespace:manage-on-call"
)

func now() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }

func entry(ns namespace.Name) audit.Entry {
	return opsmem.Entry(ns, "alice", action, ns.String(), now())
}

func setup(t *testing.T, h Harness, ns namespace.Name) namespace.Fence {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), ns)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	return fence
}

func sample(id string, ns namespace.Name, name string, members ...string) oncall.Schedule {
	return oncall.Schedule{
		ID: id, Namespace: ns, Name: name, Zone: "UTC", Source: oncall.SourceLocal,
		External: oncall.External{Provider: "", Key: ""},
		Layers: []oncall.Layer{{
			Name: "primary", Members: members, Start: now(), Turn: 7 * 24 * time.Hour, Hours: nil,
		}},
		Overrides: nil, Copied: nil, Synced: time.Time{}, Version: 0,
	}
}

func external(id string, ns namespace.Name, name string) oncall.Schedule {
	s := sample(id, ns, name)
	s.Source, s.External, s.Layers = oncall.SourceExternal, oncall.External{Provider: "pager", Key: "P1"}, nil

	return s
}

func create(t *testing.T, h Harness, fence namespace.Fence, s oncall.Schedule) {
	t.Helper()

	if err := h.Schedules.Create(t.Context(), s, fence, entry(s.Namespace)); err != nil {
		t.Fatalf("Create %s: %v", s.ID, err)
	}
}

// getNetwork returns the schedule network of the namespace shop.
func getNetwork(t *testing.T, h Harness) oncall.Schedule {
	t.Helper()

	s, err := h.Schedules.Get(t.Context(), oncall.Ref{Namespace: shop, Name: "network"})
	if err != nil {
		t.Fatalf("Get %s/network: %v", shop, err)
	}

	return s
}

func override(id, who string) oncall.Override {
	return oncall.Override{
		ID: id, Principal: who, Start: now(), End: now().Add(time.Hour), Reason: "swap", CreatedBy: who,
	}
}

func testCreateGet(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h, shop)
	create(t, h, fence, sample("ocs-1", shop, "network", "alice", "bob"))

	got := getNetwork(t, h)
	if got.ID != "ocs-1" || got.Version != 1 || len(got.Layers[0].Members) != 2 {
		t.Fatalf("Get = %+v, want the created schedule with version 1", got)
	}

	_, err := h.Schedules.Get(t.Context(), oncall.Ref{Namespace: shop, Name: "none"})
	if !errors.Is(err, oncall.ErrNotFound) {
		t.Fatalf("Get unknown: err = %v, want ErrNotFound", err)
	}
}

func testNameUnique(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h, shop)
	other := setup(t, h, platform)
	create(t, h, fence, sample("ocs-1", shop, "network", "alice"))
	create(t, h, other, sample("ocs-2", platform, "network", "bob"))

	err := h.Schedules.Create(t.Context(), sample("ocs-3", shop, "network", "carol"), fence, entry(shop))
	if !errors.Is(err, oncall.ErrExists) {
		t.Fatalf("Create with a used name: err = %v, want ErrExists", err)
	}
}

func testUpdateConflict(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h, shop)
	create(t, h, fence, sample("ocs-1", shop, "network", "alice"))

	s := getNetwork(t, h)
	s.Layers[0].Members = []string{"alice", "bob"}

	if err := h.Schedules.Update(t.Context(), s, fence, entry(shop)); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if err := h.Schedules.Update(t.Context(), s, fence, entry(shop)); !errors.Is(err, oncall.ErrConflict) {
		t.Fatalf("stale Update: err = %v, want ErrConflict", err)
	}
}

func testOverrides(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h, shop)
	create(t, h, fence, sample("ocs-1", shop, "network", "alice"))
	ref := oncall.Ref{Namespace: shop, Name: "network"}

	for _, o := range []oncall.Override{override("ovr-1", "bob"), override("ovr-2", "carol")} {
		if err := h.Schedules.AddOverride(t.Context(), ref, o, fence, entry(shop)); err != nil {
			t.Fatalf("AddOverride %s: %v", o.ID, err)
		}
	}

	got := getNetwork(t, h)
	if len(got.Overrides) != 2 || got.Version != 3 {
		t.Fatalf("Get = %d overrides, version %d; want 2 and 3", len(got.Overrides), got.Version)
	}

	bad := override("ovr-3", "dave")
	bad.Reason = ""

	if err := h.Schedules.AddOverride(t.Context(), ref, bad, fence, entry(shop)); !errors.Is(err, oncall.ErrInvalid) {
		t.Fatalf("AddOverride with no reason: err = %v, want ErrInvalid", err)
	}
}

func testCopyLocal(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h, shop)
	create(t, h, fence, sample("ocs-1", shop, "network", "alice"))

	err := h.Schedules.ReplaceCopy(t.Context(), oncall.Ref{Namespace: shop, Name: "network"}, nil, now(), fence)
	if !errors.Is(err, oncall.ErrInvalid) {
		t.Fatalf("ReplaceCopy of a local schedule: err = %v, want ErrInvalid", err)
	}
}

func testCopyExternal(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h, shop)
	create(t, h, fence, external("ocs-1", shop, "network"))

	ref := oncall.Ref{Namespace: shop, Name: "network"}
	shifts := []oncall.Shift{{
		Schedule: "ocs-1", Principal: "alice", Display: "Alice", Start: now(), End: now().Add(time.Hour),
		Layer: oncall.OriginExternal,
	}}

	if err := h.Schedules.ReplaceCopy(t.Context(), ref, shifts, now(), fence); err != nil {
		t.Fatalf("ReplaceCopy: %v", err)
	}

	got := getNetwork(t, h)
	if len(got.Copied) != 1 || !got.Synced.Equal(now()) {
		t.Fatalf("Get = %d copied shifts, synced %s; want 1 at %s", len(got.Copied), got.Synced, now())
	}

	entries, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil || len(entries) != 1 {
		t.Fatalf("audit entries = %d (%v), want only the create", len(entries), err)
	}
}

func testForPrincipal(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h, shop)
	other := setup(t, h, platform)
	create(t, h, fence, sample("ocs-1", shop, "network", "alice"))
	create(t, h, other, sample("ocs-2", platform, "db", "bob"))
	create(t, h, other, sample("ocs-3", platform, "edge", "carol"))

	err := h.Schedules.AddOverride(
		t.Context(),
		oncall.Ref{Namespace: platform, Name: "edge"},
		override("ovr-1", "alice"),
		other,
		entry(platform),
	)
	if err != nil {
		t.Fatalf("AddOverride: %v", err)
	}

	got, err := h.Schedules.ForPrincipal(t.Context(), "alice")
	if err != nil || len(got) != 2 || got[0].Name != "edge" || got[1].Name != "network" {
		t.Fatalf("ForPrincipal = %d schedules (%v), want platform/edge then shop/network", len(got), err)
	}
}

func testDelete(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h, shop)
	create(t, h, fence, sample("ocs-1", shop, "network", "alice"))

	ref := oncall.Ref{Namespace: shop, Name: "network"}
	if err := h.Schedules.Delete(t.Context(), ref, fence, entry(shop)); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	create(t, h, fence, sample("ocs-2", shop, "network", "bob"))

	err := h.Schedules.Delete(t.Context(), oncall.Ref{Namespace: shop, Name: "none"}, fence, entry(shop))
	if !errors.Is(err, oncall.ErrNotFound) {
		t.Fatalf("Delete unknown: err = %v, want ErrNotFound", err)
	}
}

func testStaleFence(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h, shop)
	if _, err := h.Domain.BumpFence(t.Context(), fence); err != nil {
		t.Fatalf("BumpFence: %v", err)
	}

	err := h.Schedules.Create(t.Context(), sample("ocs-1", shop, "network", "alice"), fence, entry(shop))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Create: err = %v, want namespace.ErrFenced", err)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil || len(entries) != 0 {
		t.Fatalf("audit entries = %d (%v), want none", len(entries), err)
	}
}

func testHandoffs(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h, shop)
	create(t, h, fence, sample("ocs-1", shop, "network", "alice", "bob"))

	for i, note := range []string{"Quiet week", "Disk alert on db-2 is known"} {
		hd := oncall.Handoff{
			Schedule: "ocs-1",
			At:       now().Add(time.Duration(i) * time.Hour),
			From:     "alice",
			To:       "bob",
			Note:     note,
			By:       "alice",
		}
		if err := h.Handoffs.AddHandoff(t.Context(), hd, fence, entry(shop)); err != nil {
			t.Fatalf("AddHandoff: %v", err)
		}
	}

	empty := oncall.Handoff{Schedule: "ocs-1", At: now(), From: "alice", To: "bob", Note: "", By: "alice"}
	if err := h.Handoffs.AddHandoff(t.Context(), empty, fence, entry(shop)); !errors.Is(err, oncall.ErrInvalid) {
		t.Fatalf("AddHandoff with no note: err = %v, want ErrInvalid", err)
	}

	got, err := h.Handoffs.Handoffs(t.Context(), "ocs-1", now(), now().Add(2*time.Hour))
	if err != nil || len(got) != 2 || got[0].Note != "Disk alert on db-2 is known" {
		t.Fatalf("Handoffs = %+v (%v), want two, newest first", got, err)
	}
}
