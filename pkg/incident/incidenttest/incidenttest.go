// Package incidenttest is the conformance suite for incident.Store
// implementations. Every store adapter runs it:
//
//	func TestIncidentConformance(t *testing.T) {
//		t.Parallel()
//		incidenttest.Run(t, func(t *testing.T) incidenttest.Harness { return newHarness(t) })
//	}
//
// A reference adapter in memory, in the tests of this package, runs the
// suite, so each case runs before the first SQL adapter.
package incidenttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/incident"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Harness is one fresh, empty database for one case.
type Harness struct {
	Store  incident.Store
	Domain Domain
}

// Domain makes the rows that an incident needs and that other ports own,
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
		{"create stores an incident and get returns it", testCreateGet},
		{"update on an older version is ErrConflict", testUpdateConflict},
		{"a closed incident does not change", testClosed},
		{"list filters by state and unit, newest first", testList},
		{"notes list oldest first, also after the close", testNotes},
		{"a stale fence changes nothing", testStaleFence},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

const shop = namespace.Name("shop")

func now() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }

func entry() audit.Entry {
	return opsmem.Entry(shop, "alice", "incident:respond", shop.String(), now())
}

func setup(t *testing.T, h Harness) namespace.Fence {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), shop)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	return fence
}

func sample(id string, opened time.Time, units ...string) incident.Incident {
	return incident.Incident{
		ID:             id,
		Namespace:      shop,
		Title:          "Checkout errors",
		Severity:       2,
		State:          incident.StateOpen,
		DetectedAt:     opened,
		OpenedAt:       opened,
		AcknowledgedAt: time.Time{},
		MitigatedAt:    time.Time{},
		ResolvedAt:     time.Time{},
		ClosedAt:       time.Time{},
		OpenedBy:       "alice",
		Commander:      "",
		OnCall:         "schedule:network",
		Units:          units,
		Jobs:           nil,
		Reviews:        nil,
		Impact:         "",
		RootCause:      "",
		Lessons:        "",
		Reports:        nil,
		Version:        0,
		Classification: incident.Classification{
			Major:        false,
			Criteria:     nil,
			ClassifiedBy: "",
			ClassifiedAt: time.Time{},
		},
	}
}

func create(t *testing.T, h Harness, fence namespace.Fence, in incident.Incident) {
	t.Helper()

	if err := h.Store.Create(t.Context(), in, fence, entry()); err != nil {
		t.Fatalf("Create %s: %v", in.ID, err)
	}
}

func testCreateGet(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("inc-1", now(), "opentofu:shop/network"))

	got, err := h.Store.Get(t.Context(), "inc-1")
	if err != nil || got.Version != 1 || got.OnCall != "schedule:network" || len(got.Units) != 1 {
		t.Fatalf("Get = %+v (%v), want the created incident with version 1", got, err)
	}

	if err := h.Store.Create(t.Context(), sample("inc-1", now()), fence, entry()); !errors.Is(err, incident.ErrExists) {
		t.Fatalf("second Create: err = %v, want ErrExists", err)
	}

	if _, err := h.Store.Get(t.Context(), "inc-none"); !errors.Is(err, incident.ErrNotFound) {
		t.Fatalf("Get unknown: err = %v, want ErrNotFound", err)
	}
}

func testUpdateConflict(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("inc-1", now()))

	in, err := h.Store.Get(t.Context(), "inc-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	acked, err := incident.Apply(in, incident.StepAcknowledge, "bob", now())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if err := h.Store.Update(t.Context(), acked, fence, entry()); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if err := h.Store.Update(t.Context(), acked, fence, entry()); !errors.Is(err, incident.ErrConflict) {
		t.Fatalf("stale Update: err = %v, want ErrConflict", err)
	}
}

func testClosed(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	in := sample("inc-1", now())
	in.State, in.ClosedAt, in.RootCause, in.Lessons = incident.StateClosed, now(), "cause", "lessons"
	create(t, h, fence, in)

	in.Version, in.Title = 1, "Changed"
	if err := h.Store.Update(t.Context(), in, fence, entry()); !errors.Is(err, incident.ErrTransition) {
		t.Fatalf("Update of a closed incident: err = %v, want ErrTransition", err)
	}
}

func testList(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("inc-1", now(), "opentofu:shop/network"))
	create(t, h, fence, sample("inc-2", now().Add(time.Hour), "opentofu:shop/db"))

	got, err := h.Store.List(t.Context(), incident.Filter{Namespace: shop, State: "", Unit: "", After: "", Limit: 0})
	if err != nil || len(got) != 2 || got[0].ID != "inc-2" {
		t.Fatalf("List = %d (%v), want inc-2 then inc-1", len(got), err)
	}

	got, err = h.Store.List(
		t.Context(),
		incident.Filter{Namespace: shop, State: "", Unit: "opentofu:shop/network", After: "", Limit: 0},
	)
	if err != nil || len(got) != 1 || got[0].ID != "inc-1" {
		t.Fatalf("List by unit = %d (%v), want inc-1", len(got), err)
	}

	got, err = h.Store.List(
		t.Context(),
		incident.Filter{Namespace: shop, State: incident.StateClosed, Unit: "", After: "", Limit: 0},
	)
	if err != nil || len(got) != 0 {
		t.Fatalf("List closed = %d (%v), want none", len(got), err)
	}
}

func testNotes(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	in := sample("inc-1", now())
	in.State, in.ClosedAt, in.RootCause, in.Lessons = incident.StateClosed, now(), "cause", "lessons"
	create(t, h, fence, in)

	for i, text := range []string{"Paged the network team", "Rolled back"} {
		n := incident.Note{Incident: "inc-1", At: now().Add(time.Duration(i) * time.Minute), By: "bob", Text: text}
		if err := h.Store.AddNote(t.Context(), n, fence, entry()); err != nil {
			t.Fatalf("AddNote: %v", err)
		}
	}

	empty := incident.Note{Incident: "inc-1", At: now(), By: "bob", Text: ""}
	if err := h.Store.AddNote(t.Context(), empty, fence, entry()); !errors.Is(err, incident.ErrInvalid) {
		t.Fatalf("AddNote with no text: err = %v, want ErrInvalid", err)
	}

	got, err := h.Store.Notes(t.Context(), "inc-1")
	if err != nil || len(got) != 2 || got[0].Text != "Paged the network team" {
		t.Fatalf("Notes = %+v (%v), want two, oldest first", got, err)
	}
}

func testStaleFence(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	if _, err := h.Domain.BumpFence(t.Context(), fence); err != nil {
		t.Fatalf("BumpFence: %v", err)
	}

	err := h.Store.Create(t.Context(), sample("inc-1", now()), fence, entry())
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Create: err = %v, want namespace.ErrFenced", err)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil || len(entries) != 0 {
		t.Fatalf("audit entries = %d (%v), want none", len(entries), err)
	}
}
