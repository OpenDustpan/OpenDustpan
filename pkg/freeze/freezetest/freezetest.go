// Package freezetest is the conformance suite for freeze.Store and
// freeze.Sink implementations. Every store adapter runs it:
//
//	func TestFreezeConformance(t *testing.T) {
//		t.Parallel()
//		freezetest.Run(t, func(t *testing.T) freezetest.Harness { return newHarness(t) })
//	}
//
// A reference adapter in memory, in the tests of this package, runs the
// suite, so each case runs before the first SQL adapter.
package freezetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/freeze"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Harness is one fresh, empty database for one case.
type Harness struct {
	Schedules freeze.Store
	Sink      freeze.Sink
	Domain    Domain
}

// Freeze is a stored freeze as the dispatch check reads it.
type Freeze struct {
	ID       string
	Schedule string
	Start    time.Time
	End      time.Time
	// Ended is true when a person ended the freeze early.
	Ended bool
}

// Domain makes the rows that other ports own, reads the stored freezes and
// the audit entries, and ends a freeze as a person does.
type Domain interface {
	// CreateNamespace creates the namespace name and returns its fence.
	CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error)
	// BumpFence increments the failover version of the namespace of fence.
	BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error)
	// AuditEntries returns the audit entries of scope, in append order.
	AuditEntries(ctx context.Context, scope namespace.Name) ([]audit.Entry, error)
	// Freezes returns the freezes of ns, ordered by start.
	Freezes(ctx context.Context, ns namespace.Name) ([]Freeze, error)
	// EndFreeze ends the freeze id at, as a person does.
	EndFreeze(ctx context.Context, id string, at time.Time) error
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
		{"create stores a schedule and get returns it", testCreateGet},
		{"a second create with one ID is ErrExists", testCreateExists},
		{"update on an older version is ErrConflict", testUpdateConflict},
		{"a stale fence changes nothing", testStaleFence},
		{"list returns the schedules of one namespace", testList},
		{"replace adds the occurrences once", testReplaceIdempotent},
		{"replace changes and deletes freezes that did not start", testReplaceFuture},
		{"a freeze that started never changes", testReplaceKeepsStarted},
		{"a freeze that a person ended does not come back", testReplaceKeepsEnded},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

const (
	shop   = namespace.Name("shop")
	action = "namespace:manage-freezes"
)

func now() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }

func entry() audit.Entry { return opsmem.Entry(shop, "alice", action, shop.String(), now()) }

func setup(t *testing.T, h Harness) namespace.Fence {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), shop)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	return fence
}

func sample(id string) freeze.Schedule {
	return freeze.Schedule{
		ID:        id,
		Namespace: shop,
		Name:      "Weekend",
		Reason:    "No changes at the weekend",
		Kind:      freeze.KindCalendar,
		Window:    freeze.Window{Start: time.Time{}, End: time.Time{}},
		Weekly:    freeze.Weekly{Days: 0, Start: 0, Length: 0, Zone: ""},
		Calendar:  freeze.CalendarRule{DaysOff: true, Holidays: true, OutsideHours: false, DaysBefore: 0},
		From:      now(),
		Until:     time.Time{},
		Enabled:   true,
		Notice:    0,
		CreatedBy: "alice",
		Version:   0,
	}
}

func occ(startDays, endDays int) freeze.Occurrence {
	day := 24 * time.Hour

	return freeze.Occurrence{
		Schedule: "fzs-weekend",
		Start:    now().Add(time.Duration(startDays) * day),
		End:      now().Add(time.Duration(endDays) * day),
	}
}

func freezes(t *testing.T, h Harness) []Freeze {
	t.Helper()

	fs, err := h.Domain.Freezes(t.Context(), shop)
	if err != nil {
		t.Fatalf("Freezes: %v", err)
	}

	return fs
}

func replace(t *testing.T, h Harness, fence namespace.Fence, from time.Time, occs ...freeze.Occurrence) {
	t.Helper()

	if err := h.Sink.ReplaceScheduled(t.Context(), fence, "fzs-weekend", from, occs); err != nil {
		t.Fatalf("ReplaceScheduled: %v", err)
	}
}

func testCreateGet(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	if err := h.Schedules.Create(t.Context(), sample("fzs-weekend"), fence, entry()); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := h.Schedules.Get(t.Context(), "fzs-weekend")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Version != 1 || got.Kind != freeze.KindCalendar || !got.Calendar.DaysOff {
		t.Fatalf("Get = %+v, want the created schedule with version 1", got)
	}

	if _, err := h.Schedules.Get(t.Context(), "fzs-none"); !errors.Is(err, freeze.ErrNotFound) {
		t.Fatalf("Get unknown: err = %v, want ErrNotFound", err)
	}
}

func testCreateExists(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	if err := h.Schedules.Create(t.Context(), sample("fzs-weekend"), fence, entry()); err != nil {
		t.Fatalf("Create: %v", err)
	}

	err := h.Schedules.Create(t.Context(), sample("fzs-weekend"), fence, entry())
	if !errors.Is(err, freeze.ErrExists) {
		t.Fatalf("second Create: err = %v, want ErrExists", err)
	}
}

func testUpdateConflict(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	if err := h.Schedules.Create(t.Context(), sample("fzs-weekend"), fence, entry()); err != nil {
		t.Fatalf("Create: %v", err)
	}

	s := sample("fzs-weekend")
	s.Version = 1

	s.Name = "Weekends"
	if err := h.Schedules.Update(t.Context(), s, fence, entry()); err != nil {
		t.Fatalf("Update: %v", err)
	}

	err := h.Schedules.Update(t.Context(), s, fence, entry())
	if !errors.Is(err, freeze.ErrConflict) {
		t.Fatalf("stale Update: err = %v, want ErrConflict", err)
	}
}

func testStaleFence(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	if _, err := h.Domain.BumpFence(t.Context(), fence); err != nil {
		t.Fatalf("BumpFence: %v", err)
	}

	err := h.Schedules.Create(t.Context(), sample("fzs-weekend"), fence, entry())
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Create: err = %v, want namespace.ErrFenced", err)
	}

	err = h.Sink.ReplaceScheduled(t.Context(), fence, "fzs-weekend", now(), []freeze.Occurrence{occ(1, 2)})
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("ReplaceScheduled: err = %v, want namespace.ErrFenced", err)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil || len(entries) != 0 || len(freezes(t, h)) != 0 {
		t.Fatalf("after fenced writes: %d entries (%v), want none", len(entries), err)
	}
}

func testList(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	for _, id := range []string{"fzs-b", "fzs-a"} {
		if err := h.Schedules.Create(t.Context(), sample(id), fence, entry()); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	got, err := h.Schedules.List(t.Context(), shop)
	if err != nil || len(got) != 2 || got[0].ID != "fzs-a" {
		t.Fatalf("List = %v (%v), want fzs-a then fzs-b", got, err)
	}

	got, err = h.Schedules.List(t.Context(), "other")
	if err != nil || len(got) != 0 {
		t.Fatalf("List of another namespace = %v (%v), want none", got, err)
	}
}

func testReplaceIdempotent(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	replace(t, h, fence, now(), occ(1, 2), occ(8, 9))
	replace(t, h, fence, now(), occ(1, 2), occ(8, 9))

	fs := freezes(t, h)
	if len(fs) != 2 || fs[0].ID != freeze.OccurrenceID("fzs-weekend", occ(1, 2).Start) ||
		fs[0].Schedule != "fzs-weekend" {
		t.Fatalf("freezes = %+v, want two with stable IDs", fs)
	}
}

func testReplaceFuture(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	replace(t, h, fence, now(), occ(1, 2), occ(8, 9))

	changed := occ(1, 3)
	replace(t, h, fence, now(), changed)

	fs := freezes(t, h)
	if len(fs) != 1 || !fs[0].End.Equal(changed.End) {
		t.Fatalf("freezes = %+v, want one with the new end", fs)
	}
}

func testReplaceKeepsStarted(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	replace(t, h, fence, now(), occ(1, 3))

	later := now().Add(48 * time.Hour)
	replace(t, h, fence, later)

	fs := freezes(t, h)
	if len(fs) != 1 || !fs[0].End.Equal(occ(1, 3).End) {
		t.Fatalf("freezes = %+v, want the started freeze unchanged", fs)
	}
}

func testReplaceKeepsEnded(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	o := occ(1, 3)
	replace(t, h, fence, now(), o)

	later := now().Add(36 * time.Hour)
	if err := h.Domain.EndFreeze(t.Context(), freeze.OccurrenceID(o.Schedule, o.Start), later); err != nil {
		t.Fatalf("EndFreeze: %v", err)
	}

	replace(t, h, fence, later.Add(time.Hour), o)

	fs := freezes(t, h)
	if len(fs) != 1 || !fs[0].Ended {
		t.Fatalf("freezes = %+v, want the ended freeze only", fs)
	}
}
