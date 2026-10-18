// Package calendartest is the conformance suite for calendar.Store
// implementations. Every store adapter runs it:
//
//	func TestCalendarConformance(t *testing.T) {
//		t.Parallel()
//		calendartest.Run(t, func(t *testing.T) calendartest.Harness { return newHarness(t) })
//	}
//
// A reference adapter in memory, in the tests of this package, runs the
// suite, so each case runs before the first SQL adapter.
package calendartest

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/calendar"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Harness is one fresh, empty database for one case.
type Harness struct {
	Store  calendar.Store
	Domain Domain
}

// Domain makes the rows that a calendar needs and that other ports own,
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
		{"create stores a calendar and get returns it", testCreateGet},
		{"get of an unknown ID is ErrNotFound", testGetUnknown},
		{"a second create with one ID is ErrExists", testCreateExists},
		{"update on an older version is ErrConflict", testUpdateConflict},
		{"a stale fence changes nothing", testStaleFence},
		{"resolve takes the first assigned scope of the chain", testResolveChain},
		{"resolve with no assignment gives the default", testResolveDefault},
		{"a calendar in use cannot be deleted", testDeleteInUse},
		{"the default calendar cannot be deleted", testDeleteDefault},
		{"a failed feed read keeps the holidays of the last good read", testFeedKeepsLastGood},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

const shop = namespace.Name("shop")

func now() time.Time {
	return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
}

// noFence is the fence of a write that no namespace scopes.
func noFence() namespace.Fence {
	return namespace.Fence{Namespace: "", FailoverVersion: 0}
}

func entry(action string) audit.Entry {
	return opsmem.Entry(shop, "alice", action, shop.String(), now())
}

func setup(t *testing.T, h Harness) namespace.Fence {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), shop)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	return fence
}

func sample(id calendar.ID) calendar.Calendar {
	c := calendar.Default()
	c.ID = id
	c.Name = "Berlin"
	c.Zone = "Europe/Berlin"
	c.Holidays = []calendar.Holiday{
		{
			Date:   calendar.Date{Year: 2026, Month: 12, Day: 24},
			Name:   "Christmas Eve",
			Source: calendar.SourceManual,
			Feed:   "",
		},
	}

	var unread calendar.FeedStatus

	c.Feeds = []calendar.Feed{{ID: "feed-1", URL: "https://holidays.example.com/de.ics", Status: unread}}

	return c
}

func create(t *testing.T, h Harness, fence namespace.Fence, c calendar.Calendar) {
	t.Helper()

	owner := calendar.Scope{Namespace: shop, Group: ""}
	if err := h.Store.Create(t.Context(), owner, c, fence, entry("namespace:manage-calendars")); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func testCreateGet(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("cal-berlin"))

	got, err := h.Store.Get(t.Context(), "cal-berlin")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Zone != "Europe/Berlin" || len(got.Holidays) != 1 || len(got.Feeds) != 1 {
		t.Fatalf("Get = %+v, want the created calendar", got)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil || len(entries) != 1 {
		t.Fatalf("audit entries = %d (%v), want 1", len(entries), err)
	}
}

func testGetUnknown(t *testing.T, h Harness) {
	t.Helper()

	if _, err := h.Store.Get(t.Context(), "cal-none"); !errors.Is(err, calendar.ErrNotFound) {
		t.Fatalf("Get: err = %v, want ErrNotFound", err)
	}
}

func testCreateExists(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("cal-berlin"))

	owner := calendar.Scope{Namespace: shop, Group: ""}

	err := h.Store.Create(t.Context(), owner, sample("cal-berlin"), fence, entry("namespace:manage-calendars"))
	if !errors.Is(err, calendar.ErrExists) {
		t.Fatalf("second Create: err = %v, want ErrExists", err)
	}
}

func testUpdateConflict(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("cal-berlin"))

	c, err := h.Store.Get(t.Context(), "cal-berlin")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	c.Name = "Berlin office"

	err = h.Store.Update(t.Context(), c, fence, entry("namespace:manage-calendars"))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	c.Name = "Stale"

	err = h.Store.Update(t.Context(), c, fence, entry("namespace:manage-calendars"))
	if !errors.Is(err, calendar.ErrConflict) {
		t.Fatalf("stale Update: err = %v, want ErrConflict", err)
	}
}

func testStaleFence(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	if _, err := h.Domain.BumpFence(t.Context(), fence); err != nil {
		t.Fatalf("BumpFence: %v", err)
	}

	owner := calendar.Scope{Namespace: shop, Group: ""}

	err := h.Store.Create(t.Context(), owner, sample("cal-berlin"), fence, entry("namespace:manage-calendars"))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Create: err = %v, want namespace.ErrFenced", err)
	}

	_, err = h.Store.Get(t.Context(), "cal-berlin")
	if !errors.Is(err, calendar.ErrNotFound) {
		t.Fatalf("Get after a fenced create: err = %v, want ErrNotFound", err)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil || len(entries) != 0 {
		t.Fatalf("audit entries = %d (%v), want 0", len(entries), err)
	}
}

func testResolveChain(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("cal-berlin"))

	team := sample("cal-team")
	team.Zone = "America/New_York"

	groupOwner := calendar.Scope{Namespace: "", Group: "network"}
	err := h.Store.Create(t.Context(), groupOwner, team, noFence(), entry("server:manage-groups"))
	if err != nil {
		t.Fatalf("Create the team calendar: %v", err)
	}

	assign := func(s calendar.Scope, id calendar.ID) {
		a := calendar.Assignment{Scope: s, Calendar: id}
		if assignErr := h.Store.Assign(t.Context(), a, fence, entry("namespace:manage-calendars")); assignErr != nil {
			t.Fatalf("Assign %s: %v", s, assignErr)
		}
	}

	nsScope := calendar.Scope{Namespace: shop, Group: ""}
	groupScope := calendar.Scope{Namespace: "", Group: "network"}

	assign(nsScope, "cal-berlin")

	a := calendar.Assignment{Scope: groupScope, Calendar: "cal-team"}

	err = h.Store.Assign(t.Context(), a, noFence(), entry("server:manage-groups"))
	if err != nil {
		t.Fatalf("Assign the team calendar: %v", err)
	}

	got, err := h.Store.Resolve(t.Context(), []calendar.Scope{groupScope, nsScope})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got.Calendar.ID != "cal-team" || got.From != groupScope {
		t.Fatalf("Resolve = %s from %s, want cal-team from the group", got.Calendar.ID, got.From)
	}

	got, err = h.Store.Resolve(t.Context(), []calendar.Scope{{Namespace: "", Group: "other"}, nsScope})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got.Calendar.ID != "cal-berlin" || got.From != nsScope {
		t.Fatalf("Resolve = %s from %s, want cal-berlin from the namespace", got.Calendar.ID, got.From)
	}
}

func testResolveDefault(t *testing.T, h Harness) {
	t.Helper()

	setup(t, h)

	got, err := h.Store.Resolve(t.Context(), []calendar.Scope{{Namespace: shop, Group: ""}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got.Calendar.ID != calendar.DefaultID || got.From != calendar.Installation() {
		t.Fatalf("Resolve = %s from %s, want the default", got.Calendar.ID, got.From)
	}
}

func testDeleteInUse(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("cal-berlin"))

	a := calendar.Assignment{Scope: calendar.Scope{Namespace: shop, Group: ""}, Calendar: "cal-berlin"}
	if err := h.Store.Assign(t.Context(), a, fence, entry("namespace:manage-calendars")); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	err := h.Store.Delete(t.Context(), "cal-berlin", fence, entry("namespace:manage-calendars"))
	if !errors.Is(err, calendar.ErrInUse) {
		t.Fatalf("Delete: err = %v, want ErrInUse", err)
	}
}

func testDeleteDefault(t *testing.T, h Harness) {
	t.Helper()

	err := h.Store.Delete(t.Context(), calendar.DefaultID, noFence(), entry("server:manage-calendars"))
	if !errors.Is(err, calendar.ErrInvalid) {
		t.Fatalf("Delete default: err = %v, want ErrInvalid", err)
	}
}

func testFeedKeepsLastGood(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, sample("cal-berlin"))

	good := []calendar.Holiday{
		{
			Date:   calendar.Date{Year: 2026, Month: 12, Day: 25},
			Name:   "Christmas Day",
			Source: calendar.SourceFeed,
			Feed:   "feed-1",
		},
		{
			Date:   calendar.Date{Year: 2026, Month: 12, Day: 26},
			Name:   "Boxing Day",
			Source: calendar.SourceFeed,
			Feed:   "feed-1",
		},
	}
	ok := calendar.FeedStatus{ReadAt: now(), Holidays: 2, Skipped: 1, Error: "", ETag: "v1"}

	if err := h.Store.ReplaceFeedHolidays(t.Context(), "cal-berlin", "feed-1", good, ok); err != nil {
		t.Fatalf("ReplaceFeedHolidays: %v", err)
	}

	failed := calendar.FeedStatus{
		ReadAt:   now().Add(time.Hour),
		Holidays: 0,
		Skipped:  0,
		Error:    "feed unreachable",
		ETag:     "",
	}
	if err := h.Store.ReplaceFeedHolidays(t.Context(), "cal-berlin", "feed-1", nil, failed); err != nil {
		t.Fatalf("ReplaceFeedHolidays with an error: %v", err)
	}

	got, err := h.Store.Get(t.Context(), "cal-berlin")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(got.Holidays) != 3 {
		t.Fatalf("holidays = %d, want 1 manual and 2 from the last good read", len(got.Holidays))
	}

	if got.Feeds[0].Status.Error == "" || got.Feeds[0].Status.ETag != "v1" {
		t.Fatalf("feed status = %+v, want the error and the ETag of the last good read", got.Feeds[0].Status)
	}

	err = h.Store.ReplaceFeedHolidays(t.Context(), "cal-berlin", "feed-none", nil, failed)
	if !errors.Is(err, calendar.ErrNotFound) {
		t.Fatalf("unknown feed: err = %v, want ErrNotFound", err)
	}
}
