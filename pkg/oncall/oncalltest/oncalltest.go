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
	"fmt"
	"testing"
	"time"

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
		{"ended overrides do not count toward the cap", testEndedOverrides},
		{"update keeps the copied shifts", testUpdateKeepsCopy},
		{"create with copied shifts is ErrInvalid", testCreateWithCopy},
		{"update to another namespace is ErrInvalid", testUpdateMove},
		{"handoff notes list newest first", testHandoffs},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}

	t.Run("each write refuses a fence that is not of the row", func(t *testing.T) {
		t.Parallel()
		testFenceScope(t, newHarness)
	})
}

const (
	shop     = namespace.Name("shop")
	platform = namespace.Name("platform")
	action   = "namespace:manage-on-call"
)

func now() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }

// entry returns the audit entry of a write of alice in the namespace ns.
func entry(ns namespace.Name) audit.Entry {
	return audit.Entry{
		At: now(), Namespace: ns, Subject: "alice", IDPSubject: "", TokenID: "",
		Action: action, Object: ns.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "192.0.2.1", UserAgent: "suite", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "req-suite", ObjectID: "", PrevHash: "", Hash: "",
		Sequence: 0, FenceVersion: 0,
	}
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

func external(id, name string) oncall.Schedule {
	s := sample(id, shop, name)
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
		CreatedAt: now(),
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

	for _, who := range []string{"bob", "carol"} {
		o := override("ovr-"+who, who)
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

	err := h.Schedules.AddOverride(t.Context(), ref, bad, fence, entry(shop))
	if !errors.Is(err, oncall.ErrInvalid) {
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
	create(t, h, fence, external("ocs-1", "network"))

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

// write is one write of the store with the fence fence.
type write struct {
	name string
	// scoped is the error for a fence that does not belong to the row.
	scoped error
	run    func(t *testing.T, h Harness, fence namespace.Fence) error
}

func writes() []write {
	network := oncall.Ref{Namespace: shop, Name: "network"}
	feed := oncall.Ref{Namespace: shop, Name: "feed"}

	return []write{
		{"Create", oncall.ErrInvalid, func(t *testing.T, h Harness, fence namespace.Fence) error {
			t.Helper()

			return h.Schedules.Create(t.Context(), sample("ocs-9", shop, "edge", "alice"), fence, entry(shop))
		}},
		{"Update", oncall.ErrNotFound, func(t *testing.T, h Harness, fence namespace.Fence) error {
			t.Helper()

			s := getNetwork(t, h)
			s.Layers[0].Members = []string{"bob"}

			return h.Schedules.Update(t.Context(), s, fence, entry(shop))
		}},
		{"Delete", oncall.ErrNotFound, func(t *testing.T, h Harness, fence namespace.Fence) error {
			t.Helper()

			return h.Schedules.Delete(t.Context(), network, fence, entry(shop))
		}},
		{"AddOverride", oncall.ErrNotFound, func(t *testing.T, h Harness, fence namespace.Fence) error {
			t.Helper()

			return h.Schedules.AddOverride(t.Context(), network, override("ovr-9", "bob"), fence, entry(shop))
		}},
		{"ReplaceCopy", oncall.ErrNotFound, func(t *testing.T, h Harness, fence namespace.Fence) error {
			t.Helper()

			return h.Schedules.ReplaceCopy(t.Context(), feed, nil, now(), fence)
		}},
		{"AddHandoff", oncall.ErrNotFound, func(t *testing.T, h Harness, fence namespace.Fence) error {
			t.Helper()

			hd := oncall.Handoff{Schedule: "ocs-1", At: now(), From: "alice", To: "bob", Note: "Quiet", By: "alice"}

			return h.Handoffs.AddHandoff(t.Context(), hd, fence, entry(shop))
		}},
	}
}

func testFenceScope(t *testing.T, newHarness Factory) {
	t.Helper()

	fences := []struct {
		name  string
		fence func(t *testing.T, h Harness, own, other namespace.Fence) namespace.Fence
		stale bool
	}{
		{"zero fence", func(*testing.T, Harness, namespace.Fence, namespace.Fence) namespace.Fence {
			return namespace.Fence{Namespace: "", FailoverVersion: 0}
		}, false},
		{"fence of another namespace", func(_ *testing.T, _ Harness, _, other namespace.Fence) namespace.Fence {
			return other
		}, false},
		{"stale fence", func(t *testing.T, h Harness, own, _ namespace.Fence) namespace.Fence {
			t.Helper()

			if _, err := h.Domain.BumpFence(t.Context(), own); err != nil {
				t.Fatalf("BumpFence: %v", err)
			}

			return own
		}, true},
	}

	for _, w := range writes() {
		for _, f := range fences {
			t.Run(w.name+" with a "+f.name, func(t *testing.T) {
				t.Parallel()
				want := w.scoped
				if f.stale {
					want = namespace.ErrFenced
				}

				checkFence(t, newHarness(t), w, f.fence, want)
			})
		}
	}
}

// checkFence runs the write w with the fence that fence returns, and
// checks that it fails with want and changes nothing.
func checkFence(
	t *testing.T,
	h Harness,
	w write,
	fence func(t *testing.T, h Harness, own, other namespace.Fence) namespace.Fence,
	want error,
) {
	t.Helper()

	own := setup(t, h, shop)
	other := setup(t, h, platform)

	create(t, h, own, sample("ocs-1", shop, "network", "alice"))
	create(t, h, own, external("ocs-2", "feed"))

	before := getNetwork(t, h)

	if err := w.run(t, h, fence(t, h, own, other)); !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want %v", w.name, err, want)
	}

	after := getNetwork(t, h)
	if after.Version != before.Version || len(after.Overrides) != len(before.Overrides) {
		t.Fatalf("%s changed the schedule: version %d, want %d", w.name, after.Version, before.Version)
	}

	for ns, n := range map[namespace.Name]int{shop: 2, platform: 0} {
		entries, err := h.Domain.AuditEntries(t.Context(), ns)
		if err != nil || len(entries) != n {
			t.Fatalf("%s: audit entries of %s = %d (%v), want %d", w.name, ns, len(entries), err, n)
		}
	}
}

func testEndedOverrides(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h, shop)
	create(t, h, fence, sample("ocs-1", shop, "network", "alice"))
	ref := oncall.Ref{Namespace: shop, Name: "network"}

	for i := range oncall.MaxOverrides {
		o := override(fmt.Sprintf("ovr-%d", i), "bob")
		if err := h.Schedules.AddOverride(t.Context(), ref, o, fence, entry(shop)); err != nil {
			t.Fatalf("AddOverride %d: %v", i, err)
		}
	}

	err := h.Schedules.AddOverride(t.Context(), ref, override("ovr-full", "bob"), fence, entry(shop))
	if !errors.Is(err, oncall.ErrInvalid) {
		t.Fatalf("AddOverride above the cap: err = %v, want ErrInvalid", err)
	}

	// Every override so far ends one hour after now(). Two hours later
	// they all ended, so they do not count.
	later := now().Add(2 * time.Hour)
	o := override("ovr-later", "carol")
	o.Start, o.End, o.CreatedAt = later, later.Add(time.Hour), later

	if err := h.Schedules.AddOverride(t.Context(), ref, o, fence, entry(shop)); err != nil {
		t.Fatalf("AddOverride after the others ended: %v", err)
	}

	got := getNetwork(t, h)
	if len(got.Overrides) != 1 {
		t.Fatalf("Get = %d overrides, want only the one that did not end", len(got.Overrides))
	}

	if sh, err := got.At(later.Add(time.Minute)); err != nil || sh.Principal != "carol" {
		t.Fatalf("At = %s (%v), want carol", sh.Principal, err)
	}
}

func testUpdateKeepsCopy(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h, shop)
	create(t, h, fence, external("ocs-1", "network"))

	ref := oncall.Ref{Namespace: shop, Name: "network"}
	shifts := []oncall.Shift{{
		Schedule: "ocs-1", Principal: "alice", Display: "Alice", Start: now(), End: now().Add(time.Hour),
		Layer: oncall.OriginExternal,
	}}

	if err := h.Schedules.ReplaceCopy(t.Context(), ref, shifts, now(), fence); err != nil {
		t.Fatalf("ReplaceCopy: %v", err)
	}

	s := getNetwork(t, h)
	s.External.Key = "P2"
	s.Copied, s.Synced = nil, time.Time{}

	if err := h.Schedules.Update(t.Context(), s, fence, entry(shop)); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got := getNetwork(t, h)
	if got.External.Key != "P2" || len(got.Copied) != 1 || !got.Synced.Equal(now()) {
		t.Fatalf("Get = key %q, %d copied shifts, synced %s; want P2, 1 and %s",
			got.External.Key, len(got.Copied), got.Synced, now())
	}
}

func testCreateWithCopy(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h, shop)

	cases := []struct {
		name   string
		change func(s *oncall.Schedule)
	}{
		{"copied shifts", func(s *oncall.Schedule) {
			s.Copied = []oncall.Shift{{
				Schedule: s.ID, Principal: "alice", Display: "Alice", Start: now(), End: now().Add(time.Hour),
				Layer: oncall.OriginExternal,
			}}
		}},
		{"a sync time", func(s *oncall.Schedule) { s.Synced = now() }},
	}

	for _, tc := range cases {
		s := external("ocs-1", "network")
		tc.change(&s)

		if err := h.Schedules.Create(t.Context(), s, fence, entry(shop)); !errors.Is(err, oncall.ErrInvalid) {
			t.Fatalf("Create with %s: err = %v, want ErrInvalid", tc.name, err)
		}
	}

	_, err := h.Schedules.Get(t.Context(), oncall.Ref{Namespace: shop, Name: "network"})
	if !errors.Is(err, oncall.ErrNotFound) {
		t.Fatalf("Get after the refused creates: err = %v, want ErrNotFound", err)
	}
}

func testUpdateMove(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h, shop)
	setup(t, h, platform)
	create(t, h, fence, sample("ocs-1", shop, "network", "alice"))

	s := getNetwork(t, h)
	s.Namespace = platform

	if err := h.Schedules.Update(t.Context(), s, fence, entry(shop)); !errors.Is(err, oncall.ErrInvalid) {
		t.Fatalf("Update to another namespace: err = %v, want ErrInvalid", err)
	}
}
