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
	"reflect"
	"testing"
	"time"

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

type testCase struct {
	name string
	run  func(t *testing.T, h Harness)
}

// Run executes the conformance suite against harnesses from newHarness.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	base := []testCase{
		{"create stores a calendar and get returns it", testCreateGet},
		{"get of an unknown ID is ErrNotFound", testGetUnknown},
		{"get of a calendar of another scope is ErrNotFound", testGetOtherScope},
		{"a second create with one ID is ErrExists", testCreateExists},
		{"update on an older version is ErrConflict", testUpdateConflict},
		{"update keeps the holidays and the status of an unchanged feed", testUpdateKeepsFeed},
		{"update with the masked URL keeps the stored URL", testUpdateMaskedURL},
		{"update drops the holidays of a changed or removed feed", testUpdateDropsFeed},
		{"update cannot change the owner", testUpdateOwner},
		{"an administrator updates the default calendar", testUpdateDefault},
		{"resolve takes the first assigned scope of the chain", testResolveChain},
		{"resolve with no assignment gives the default", testResolveDefault},
		{"a cleared assignment falls back to the next scope", testAssignClear},
		{"assign of an unknown calendar is ErrNotFound", testAssignUnknown},
		{"assign of a calendar of another namespace is ErrInvalid", testAssignOtherNamespace},
		{"a calendar in use cannot be deleted", testDeleteInUse},
		{"the default calendar cannot be deleted", testDeleteDefault},
		{"delete removes the calendar", testDelete},
		{"holidays are ordered by date", testHolidayOrder},
		{"a calendar holds at most MaxHolidays manual holidays", testMaxManualHolidays},
		{"a good feed read is bounded and checked", testReplaceBounds},
		{"a failed feed read keeps the holidays of the last good read", testFeedKeepsLastGood},
		{"a not-modified feed read keeps the holidays and the ETag", testFeedNotModified},
		{"a feed status holds an error class only", testFeedErrorClass},
		{"writes of a group or the installation append to the server scope", testServerScopeEntries},
		{"a feed read of an old URL is ErrConflict and changes nothing", testFeedURLChanged},
		{"an audit entry of another namespace is refused and changes nothing", testEntryOtherNamespace},
	}

	fence := fenceCases()
	cases := make([]testCase, 0, len(base)+len(fence))
	cases = append(cases, base...)
	cases = append(cases, fence...)

	for i := range cases {
		tc := &cases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

const (
	shop    = namespace.Name("shop")
	lab     = namespace.Name("lab")
	network = "network"
	feedID  = "feed-1"

	nsAction     = "namespace:manage-calendars"
	serverAction = "server:manage-calendars"
)

func now() time.Time {
	return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
}

// noFence is the fence of a write in the server scope.
func noFence() namespace.Fence {
	return namespace.Fence{Namespace: "", FailoverVersion: 0}
}

func nsScope(ns namespace.Name) calendar.Scope {
	return calendar.Scope{Namespace: ns, Group: ""}
}

func groupScope() calendar.Scope {
	return calendar.Scope{Namespace: "", Group: network}
}

// entry returns a valid audit entry of the kind change in scope.
func entry(scope namespace.Name, action string) audit.Entry {
	return audit.Entry{
		At:           now(),
		Namespace:    scope,
		Subject:      "alice",
		IDPSubject:   "",
		TokenID:      "",
		Action:       action,
		Object:       scope.String(),
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "192.0.2.1",
		UserAgent:    "suite",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindChange,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "req-suite",
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		FenceVersion: 0,
	}
}

func nsEntry() audit.Entry     { return entry(shop, nsAction) }
func serverEntry() audit.Entry { return entry(audit.ServerScope, serverAction) }

// fixture holds the fences of the namespaces shop and lab.
type fixture struct {
	shop namespace.Fence
	lab  namespace.Fence
}

func setup(t *testing.T, h Harness) fixture {
	t.Helper()

	var f fixture

	for _, n := range []struct {
		name  namespace.Name
		fence *namespace.Fence
	}{{shop, &f.shop}, {lab, &f.lab}} {
		fence, err := h.Domain.CreateNamespace(t.Context(), n.name)
		if err != nil {
			t.Fatalf("CreateNamespace %s: %v", n.name, err)
		}

		*n.fence = fence
	}

	return f
}

func date(m time.Month, d int) calendar.Date {
	return calendar.Date{Year: 2026, Month: m, Day: d}
}

func manual(d calendar.Date, name string) calendar.Holiday {
	return calendar.Holiday{Date: d, Name: name, Source: calendar.SourceManual, Feed: ""}
}

func fromFeed(d calendar.Date, name string) calendar.Holiday {
	return calendar.Holiday{Date: d, Name: name, Source: calendar.SourceFeed, Feed: feedID}
}

const (
	feedURL  = "https://calendar.example.com/ical/t0ken/basic.ics"
	otherURL = "https://calendar.example.com/ical/other/basic.ics"
)

// sample returns a calendar of owner with one manual holiday and one feed.
func sample(id calendar.ID, owner calendar.Scope) calendar.Calendar {
	c := calendar.Default()
	c.ID = id
	c.Owner = owner
	c.Name = "Berlin"
	c.Zone = "Europe/Berlin"
	c.Holidays = []calendar.Holiday{manual(date(12, 24), "Christmas Eve")}

	c.Feeds = []calendar.Feed{{ID: feedID, URL: feedURL, Status: unread()}}

	return c
}

func create(t *testing.T, h Harness, fence namespace.Fence, c calendar.Calendar) {
	t.Helper()

	e := nsEntry()
	if c.Owner.Namespace == "" {
		e = serverEntry()
	}

	if err := h.Store.Create(t.Context(), c, fence, e); err != nil {
		t.Fatalf("Create %s: %v", c.ID, err)
	}
}

func get(t *testing.T, h Harness, scope namespace.Name, id calendar.ID) calendar.Calendar {
	t.Helper()

	c, err := h.Store.Get(t.Context(), scope, id)
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}

	return c
}

// unread is the status of a feed before its first read.
func unread() calendar.FeedStatus {
	var s calendar.FeedStatus

	return s
}

func goodStatus(n int, etag string) calendar.FeedStatus {
	return calendar.FeedStatus{ReadAt: now(), Holidays: n, Skipped: 1, Error: "", ETag: etag}
}

// status is a read of the feed feedID of feedURL with no new holidays.
func status(feedID string, s calendar.FeedStatus) calendar.FeedRead {
	return calendar.FeedRead{Feed: feedID, URL: feedURL, Holidays: nil, Status: s}
}

// replace records a good read of feed-1 of id with holidays.
func replace(t *testing.T, h Harness, fence namespace.Fence, id calendar.ID, holidays ...calendar.Holiday) {
	t.Helper()

	read := calendar.FeedRead{Feed: feedID, URL: feedURL, Holidays: holidays, Status: goodStatus(len(holidays), "v1")}

	err := h.Store.ReplaceFeedHolidays(t.Context(), id, read, fence)
	if err != nil {
		t.Fatalf("ReplaceFeedHolidays: %v", err)
	}
}

func auditCount(t *testing.T, h Harness, scope namespace.Name) int {
	t.Helper()

	entries, err := h.Domain.AuditEntries(t.Context(), scope)
	if err != nil {
		t.Fatalf("AuditEntries %s: %v", scope, err)
	}

	return len(entries)
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want %v", what, err, want)
	}
}

func testCreateGet(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)

	c := sample("cal-berlin", nsScope(shop))
	c.Feeds[0].Status = goodStatus(9, "forged")
	create(t, h, f.shop, c)

	got := get(t, h, shop, "cal-berlin")
	if got.Zone != "Europe/Berlin" || got.Owner != nsScope(shop) || got.Version != 1 ||
		len(got.Holidays) != 1 || len(got.Feeds) != 1 || got.Feeds[0].URL != feedURL {
		t.Fatalf("Get = %+v, want the created calendar at version 1", got)
	}

	if got.Feeds[0].Status != unread() {
		t.Fatalf("feed status = %+v, want a zero status after a create", got.Feeds[0].Status)
	}

	if n := auditCount(t, h, shop); n != 1 {
		t.Fatalf("audit entries = %d, want 1", n)
	}
}

func testGetUnknown(t *testing.T, h Harness) {
	t.Helper()

	_, err := h.Store.Get(t.Context(), shop, "cal-none")
	wantErr(t, "Get", err, calendar.ErrNotFound)
}

func testGetOtherScope(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)
	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))
	create(t, h, noFence(), sample("cal-team", groupScope()))

	for _, tc := range []struct {
		scope namespace.Name
		id    calendar.ID
	}{
		{lab, "cal-berlin"},
		{audit.ServerScope, "cal-berlin"},
		{shop, "cal-team"},
		{shop, calendar.DefaultID},
	} {
		_, err := h.Store.Get(t.Context(), tc.scope, tc.id)
		wantErr(t, "Get "+string(tc.id)+" in "+tc.scope.String(), err, calendar.ErrNotFound)
	}

	get(t, h, shop, "cal-berlin")
	get(t, h, audit.ServerScope, "cal-team")
	get(t, h, audit.ServerScope, calendar.DefaultID)
}

func testCreateExists(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)
	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))

	err := h.Store.Create(t.Context(), sample("cal-berlin", nsScope(shop)), f.shop, nsEntry())
	wantErr(t, "second Create", err, calendar.ErrExists)

	err = h.Store.Create(t.Context(), sample(calendar.DefaultID, calendar.Installation()), noFence(), serverEntry())
	wantErr(t, "Create of the default ID", err, calendar.ErrExists)
}

func testUpdateConflict(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)
	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))

	c := get(t, h, shop, "cal-berlin")
	c.Name = "Berlin office"

	if err := h.Store.Update(t.Context(), c, f.shop, nsEntry()); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if got := get(t, h, shop, "cal-berlin"); got.Version != 2 || got.Name != "Berlin office" {
		t.Fatalf("Get = version %d name %q, want version 2 and the new name", got.Version, got.Name)
	}

	c.Name = "Stale"

	err := h.Store.Update(t.Context(), c, f.shop, nsEntry())
	wantErr(t, "stale Update", err, calendar.ErrConflict)
}

func testUpdateKeepsFeed(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)
	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))
	replace(t, h, f.shop, "cal-berlin", fromFeed(date(12, 25), "Christmas Day"))

	c := get(t, h, shop, "cal-berlin")
	c.Name = "Berlin office"
	c.Holidays = []calendar.Holiday{manual(date(12, 31), "New Year's Eve")}
	c.Feeds[0].Status = unread()

	if err := h.Store.Update(t.Context(), c, f.shop, nsEntry()); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got := get(t, h, shop, "cal-berlin")
	if len(got.Holidays) != 2 || got.Holidays[0].Source != calendar.SourceFeed || got.Holidays[1].Date != date(12, 31) {
		t.Fatalf("holidays = %+v, want the feed holiday and the new manual holiday", got.Holidays)
	}

	if got.Feeds[0].Status.ETag != "v1" || got.Feeds[0].Status.Holidays != 1 {
		t.Fatalf("feed status = %+v, want the status of the last good read", got.Feeds[0].Status)
	}
}

func testUpdateMaskedURL(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)
	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))
	replace(t, h, f.shop, "cal-berlin", fromFeed(date(12, 25), "Christmas Day"))

	c := get(t, h, shop, "cal-berlin").Masked()

	if err := h.Store.Update(t.Context(), c, f.shop, nsEntry()); err != nil {
		t.Fatalf("Update with the masked URL: %v", err)
	}

	got := get(t, h, shop, "cal-berlin")
	if got.Feeds[0].URL != feedURL || len(got.Holidays) != 2 {
		t.Fatalf(
			"feed = %q with %d holidays, want the stored URL and its holidays",
			got.Feeds[0].URL,
			len(got.Holidays),
		)
	}
}

func testUpdateDropsFeed(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)
	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))
	replace(t, h, f.shop, "cal-berlin", fromFeed(date(12, 25), "Christmas Day"))

	c := get(t, h, shop, "cal-berlin")
	c.Feeds[0].URL = otherURL

	if err := h.Store.Update(t.Context(), c, f.shop, nsEntry()); err != nil {
		t.Fatalf("Update with a new URL: %v", err)
	}

	got := get(t, h, shop, "cal-berlin")
	if len(got.Holidays) != 1 || got.Feeds[0].Status != unread() {
		t.Fatalf("holidays = %d, status = %+v, want only the manual holiday and a zero status",
			len(got.Holidays), got.Feeds[0].Status)
	}

	read := calendar.FeedRead{
		Feed:     feedID,
		URL:      otherURL,
		Holidays: []calendar.Holiday{fromFeed(date(12, 25), "Christmas Day")},
		Status:   goodStatus(1, "v1"),
	}
	if err := h.Store.ReplaceFeedHolidays(t.Context(), "cal-berlin", read, f.shop); err != nil {
		t.Fatalf("ReplaceFeedHolidays of the new URL: %v", err)
	}

	c = get(t, h, shop, "cal-berlin")
	c.Feeds = nil

	if err := h.Store.Update(t.Context(), c, f.shop, nsEntry()); err != nil {
		t.Fatalf("Update with no feed: %v", err)
	}

	if got = get(t, h, shop, "cal-berlin"); len(got.Holidays) != 1 || len(got.Feeds) != 0 {
		t.Fatalf("Get = %d holidays and %d feeds, want 1 manual holiday and no feed", len(got.Holidays), len(got.Feeds))
	}
}

func testUpdateOwner(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)
	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))

	c := get(t, h, shop, "cal-berlin")
	c.Owner = groupScope()

	err := h.Store.Update(t.Context(), c, f.shop, nsEntry())
	wantErr(t, "Update of the owner", err, calendar.ErrInvalid)
}

func testUpdateDefault(t *testing.T, h Harness) {
	t.Helper()

	setup(t, h)

	c := get(t, h, audit.ServerScope, calendar.DefaultID)
	c.Zone = "Europe/Berlin"

	if err := h.Store.Update(t.Context(), c, noFence(), serverEntry()); err != nil {
		t.Fatalf("Update of the default: %v", err)
	}

	got := get(t, h, audit.ServerScope, calendar.DefaultID)
	if got.Zone != "Europe/Berlin" || got.Version != c.Version+1 {
		t.Fatalf("default = %s at version %d, want Europe/Berlin at version %d", got.Zone, got.Version, c.Version+1)
	}

	if n := auditCount(t, h, audit.ServerScope); n != 1 {
		t.Fatalf("server audit entries = %d, want 1", n)
	}
}

func assign(t *testing.T, h Harness, fence namespace.Fence, s calendar.Scope, id calendar.ID) {
	t.Helper()

	e := nsEntry()
	if s.Namespace == "" {
		e = serverEntry()
	}

	if err := h.Store.Assign(t.Context(), calendar.Assignment{Scope: s, Calendar: id}, fence, e); err != nil {
		t.Fatalf("Assign %s to %s: %v", id, s, err)
	}
}

func resolve(t *testing.T, h Harness, chain ...calendar.Scope) calendar.Resolved {
	t.Helper()

	got, err := h.Store.Resolve(t.Context(), chain)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	return got
}

func testResolveChain(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)
	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))

	team := sample("cal-team", groupScope())
	team.Zone = "America/New_York"
	create(t, h, noFence(), team)

	assign(t, h, f.shop, nsScope(shop), "cal-berlin")
	assign(t, h, noFence(), groupScope(), "cal-team")

	got := resolve(t, h, groupScope(), nsScope(shop))
	if got.Calendar.ID != "cal-team" || got.From != groupScope() {
		t.Fatalf("Resolve = %s from %s, want cal-team from the group", got.Calendar.ID, got.From)
	}

	got = resolve(t, h, calendar.Scope{Namespace: "", Group: "other"}, nsScope(shop))
	if got.Calendar.ID != "cal-berlin" || got.From != nsScope(shop) {
		t.Fatalf("Resolve = %s from %s, want cal-berlin from the namespace", got.Calendar.ID, got.From)
	}
}

func testResolveDefault(t *testing.T, h Harness) {
	t.Helper()

	setup(t, h)

	got := resolve(t, h, nsScope(shop))
	if got.Calendar.ID != calendar.DefaultID || got.From != calendar.Installation() {
		t.Fatalf("Resolve = %s from %s, want the default", got.Calendar.ID, got.From)
	}
}

func testAssignClear(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)
	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))
	assign(t, h, f.shop, nsScope(shop), "cal-berlin")
	assign(t, h, f.shop, nsScope(shop), "")

	got := resolve(t, h, nsScope(shop))
	if got.Calendar.ID != calendar.DefaultID || got.From != calendar.Installation() {
		t.Fatalf("Resolve = %s from %s, want the default after the clear", got.Calendar.ID, got.From)
	}

	if err := h.Store.Delete(t.Context(), "cal-berlin", f.shop, nsEntry()); err != nil {
		t.Fatalf("Delete after the clear: %v", err)
	}
}

func testAssignUnknown(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)

	a := calendar.Assignment{Scope: nsScope(shop), Calendar: "cal-none"}
	err := h.Store.Assign(t.Context(), a, f.shop, nsEntry())
	wantErr(t, "Assign of an unknown calendar", err, calendar.ErrNotFound)

	if n := auditCount(t, h, shop); n != 0 {
		t.Fatalf("audit entries = %d, want 0", n)
	}
}

func testAssignOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)

	other := sample("cal-lab", nsScope(lab))
	if err := h.Store.Create(t.Context(), other, f.lab, entry(lab, nsAction)); err != nil {
		t.Fatalf("Create in lab: %v", err)
	}

	a := calendar.Assignment{Scope: nsScope(shop), Calendar: "cal-lab"}
	err := h.Store.Assign(t.Context(), a, f.shop, nsEntry())
	wantErr(t, "Assign of a calendar of lab to shop", err, calendar.ErrInvalid)

	a.Scope = groupScope()
	err = h.Store.Assign(t.Context(), a, noFence(), serverEntry())
	wantErr(t, "Assign of a calendar of lab to a group", err, calendar.ErrInvalid)

	if got := resolve(t, h, nsScope(shop)); got.Calendar.ID != calendar.DefaultID {
		t.Fatalf("Resolve = %s, want the default", got.Calendar.ID)
	}
}

func testDeleteInUse(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)
	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))
	assign(t, h, f.shop, nsScope(shop), "cal-berlin")

	err := h.Store.Delete(t.Context(), "cal-berlin", f.shop, nsEntry())
	wantErr(t, "Delete", err, calendar.ErrInUse)
}

func testDeleteDefault(t *testing.T, h Harness) {
	t.Helper()

	err := h.Store.Delete(t.Context(), calendar.DefaultID, noFence(), serverEntry())
	wantErr(t, "Delete of the default", err, calendar.ErrInvalid)
}

func testDelete(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)
	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))

	if err := h.Store.Delete(t.Context(), "cal-berlin", f.shop, nsEntry()); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := h.Store.Get(t.Context(), shop, "cal-berlin")
	wantErr(t, "Get after Delete", err, calendar.ErrNotFound)

	err = h.Store.Delete(t.Context(), "cal-berlin", f.shop, nsEntry())
	wantErr(t, "second Delete", err, calendar.ErrNotFound)

	if n := auditCount(t, h, shop); n != 2 {
		t.Fatalf("audit entries = %d, want 2", n)
	}
}

func testHolidayOrder(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)

	c := sample("cal-berlin", nsScope(shop))
	c.Holidays = []calendar.Holiday{manual(date(12, 31), "New Year's Eve"), manual(date(12, 24), "Christmas Eve")}
	create(t, h, f.shop, c)
	replace(t, h, f.shop, "cal-berlin", fromFeed(date(12, 26), "Boxing Day"), fromFeed(date(1, 1), "New Year"))

	want := []calendar.Date{date(1, 1), date(12, 24), date(12, 26), date(12, 31)}

	got := get(t, h, shop, "cal-berlin")
	if len(got.Holidays) != len(want) {
		t.Fatalf("holidays = %d, want %d", len(got.Holidays), len(want))
	}

	for i, d := range want {
		if got.Holidays[i].Date != d {
			t.Fatalf("holiday %d = %s, want %s", i, got.Holidays[i].Date, d)
		}
	}
}

func manyHolidays(n int, mk func(calendar.Date, string) calendar.Holiday) []calendar.Holiday {
	hs := make([]calendar.Holiday, 0, n)
	start := calendar.Date{Year: 2026, Month: 1, Day: 1}

	for i := range n {
		hs = append(hs, mk(start.AddDays(i), "closed"))
	}

	return hs
}

func testMaxManualHolidays(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)

	c := sample("cal-full", nsScope(shop))
	c.Holidays = manyHolidays(calendar.MaxHolidays, manual)
	create(t, h, f.shop, c)

	c = sample("cal-over", nsScope(shop))
	c.Holidays = manyHolidays(calendar.MaxHolidays+1, manual)

	err := h.Store.Create(t.Context(), c, f.shop, nsEntry())
	wantErr(t, "Create above MaxHolidays", err, calendar.ErrInvalid)
}

func testReplaceBounds(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)
	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))

	full := manyHolidays(calendar.MaxHolidays, fromFeed)
	replace(t, h, f.shop, "cal-berlin", full...)

	if got := get(t, h, shop, "cal-berlin"); got.Feeds[0].Status.Holidays != calendar.MaxHolidays {
		t.Fatalf("status holidays = %d, want %d", got.Feeds[0].Status.Holidays, calendar.MaxHolidays)
	}

	otherFeed := fromFeed(date(5, 1), "Labor Day")
	otherFeed.Feed = "feed-2"

	failed := goodStatus(0, "")
	failed.Error = calendar.FeedTimeout

	cases := []struct {
		name     string
		feed     string
		holidays []calendar.Holiday
		status   calendar.FeedStatus
		want     error
	}{
		{
			"above MaxHolidays",
			feedID,
			manyHolidays(calendar.MaxHolidays+1, fromFeed),
			goodStatus(0, ""),
			calendar.ErrInvalid,
		},
		{"a holiday of another feed", feedID, []calendar.Holiday{otherFeed}, goodStatus(1, ""), calendar.ErrInvalid},
		{
			"a manual holiday",
			feedID,
			[]calendar.Holiday{manual(date(5, 1), "x")},
			goodStatus(1, ""),
			calendar.ErrInvalid,
		},
		{"a status with an error", feedID, nil, failed, calendar.ErrInvalid},
		{"an unknown feed", "feed-none", nil, goodStatus(0, ""), calendar.ErrNotFound},
	}

	for i := range cases {
		tc := &cases[i]
		read := calendar.FeedRead{Feed: tc.feed, URL: feedURL, Holidays: tc.holidays, Status: tc.status}

		err := h.Store.ReplaceFeedHolidays(t.Context(), "cal-berlin", read, f.shop)
		wantErr(t, tc.name, err, tc.want)
	}

	if got := get(t, h, shop, "cal-berlin"); len(got.Holidays) != calendar.MaxHolidays+1 {
		t.Fatalf("holidays = %d, want the holidays of the last good read and the manual one", len(got.Holidays))
	}

	none := calendar.FeedRead{Feed: feedID, URL: feedURL, Holidays: nil, Status: goodStatus(0, "")}

	err := h.Store.ReplaceFeedHolidays(t.Context(), "cal-none", none, f.shop)
	wantErr(t, "an unknown calendar", err, calendar.ErrNotFound)
}

func testFeedKeepsLastGood(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)
	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))
	replace(t, h, f.shop, "cal-berlin", fromFeed(date(12, 25), "Christmas Day"), fromFeed(date(12, 26), "Boxing Day"))

	failed := calendar.FeedStatus{
		ReadAt:   now().Add(time.Hour),
		Holidays: 0,
		Skipped:  0,
		Error:    calendar.FeedUnreachable,
		ETag:     "",
	}
	if err := h.Store.RecordFeedStatus(t.Context(), "cal-berlin", status(feedID, failed), f.shop); err != nil {
		t.Fatalf("RecordFeedStatus: %v", err)
	}

	got := get(t, h, shop, "cal-berlin")
	if len(got.Holidays) != 3 {
		t.Fatalf("holidays = %d, want 1 manual and 2 from the last good read", len(got.Holidays))
	}

	want := calendar.FeedStatus{
		ReadAt:   now().Add(time.Hour),
		Holidays: 2,
		Skipped:  1,
		Error:    calendar.FeedUnreachable,
		ETag:     "v1",
	}
	if got.Feeds[0].Status != want {
		t.Fatalf("feed status = %+v, want %+v", got.Feeds[0].Status, want)
	}

	err := h.Store.RecordFeedStatus(t.Context(), "cal-berlin", status("feed-none", failed), f.shop)
	wantErr(t, "unknown feed", err, calendar.ErrNotFound)

	err = h.Store.RecordFeedStatus(t.Context(), "cal-none", status(feedID, failed), f.shop)
	wantErr(t, "unknown calendar", err, calendar.ErrNotFound)
}

func testFeedNotModified(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)
	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))
	replace(t, h, f.shop, "cal-berlin", fromFeed(date(12, 25), "Christmas Day"))

	failed := calendar.FeedStatus{
		ReadAt:   now().Add(time.Hour),
		Holidays: 0,
		Skipped:  0,
		Error:    calendar.FeedTimeout,
		ETag:     "",
	}
	if err := h.Store.RecordFeedStatus(t.Context(), "cal-berlin", status(feedID, failed), f.shop); err != nil {
		t.Fatalf("RecordFeedStatus of a failed read: %v", err)
	}

	unchanged := calendar.FeedStatus{ReadAt: now().Add(2 * time.Hour), Holidays: 0, Skipped: 0, Error: "", ETag: ""}
	if err := h.Store.RecordFeedStatus(t.Context(), "cal-berlin", status(feedID, unchanged), f.shop); err != nil {
		t.Fatalf("RecordFeedStatus of a not-modified read: %v", err)
	}

	got := get(t, h, shop, "cal-berlin")
	if len(got.Holidays) != 2 {
		t.Fatalf("holidays = %d, want the manual holiday and the holiday of the last good read", len(got.Holidays))
	}

	want := calendar.FeedStatus{ReadAt: now().Add(2 * time.Hour), Holidays: 1, Skipped: 1, Error: "", ETag: "v1"}
	if got.Feeds[0].Status != want {
		t.Fatalf("feed status = %+v, want %+v", got.Feeds[0].Status, want)
	}
}

func testFeedErrorClass(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)
	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))

	raw := calendar.FeedStatus{
		ReadAt:   now(),
		Holidays: 0,
		Skipped:  0,
		Error:    "dial tcp 10.0.0.7:443: connect: connection refused",
		ETag:     "",
	}

	err := h.Store.RecordFeedStatus(t.Context(), "cal-berlin", status(feedID, raw), f.shop)
	wantErr(t, "RecordFeedStatus with a raw error text", err, calendar.ErrInvalid)

	if got := get(t, h, shop, "cal-berlin"); got.Feeds[0].Status != unread() {
		t.Fatalf("feed status = %+v, want no change", got.Feeds[0].Status)
	}
}

func testServerScopeEntries(t *testing.T, h Harness) {
	t.Helper()

	setup(t, h)
	create(t, h, noFence(), sample("cal-team", groupScope()))
	create(t, h, noFence(), sample("cal-office", calendar.Installation()))
	assign(t, h, noFence(), groupScope(), "cal-team")
	replace(t, h, noFence(), "cal-team", fromFeed(date(12, 25), "Christmas Day"))

	if n := auditCount(t, h, audit.ServerScope); n != 3 {
		t.Fatalf("server audit entries = %d, want 3: two creates and one assignment, none for the feed read", n)
	}

	if n := auditCount(t, h, shop); n != 0 {
		t.Fatalf("shop audit entries = %d, want 0", n)
	}
}

// fenceWrite is one write method of the store, in a fixture with the
// calendar cal-berlin of shop and the calendar cal-team of the group
// network, each with the feed feed-1.
type fenceWrite struct {
	name string
	// refused is the error for a fence that does not belong to the scope
	// of the row.
	refused error
	// inShop writes a row of shop; inServer writes a row of the server
	// scope.
	inShop   func(t *testing.T, h Harness, fence namespace.Fence) error
	inServer func(t *testing.T, h Harness, fence namespace.Fence) error
}

func fenceWrites() []fenceWrite {
	update := func(id calendar.ID, owner calendar.Scope, e audit.Entry) func(*testing.T, Harness, namespace.Fence) error {
		return func(t *testing.T, h Harness, fence namespace.Fence) error {
			t.Helper()

			c := sample(id, owner)
			c.Version = 1
			c.Name = "Changed"

			return h.Store.Update(t.Context(), c, fence, e)
		}
	}
	del := func(id calendar.ID, e audit.Entry) func(*testing.T, Harness, namespace.Fence) error {
		return func(t *testing.T, h Harness, fence namespace.Fence) error {
			t.Helper()

			return h.Store.Delete(t.Context(), id, fence, e)
		}
	}
	createIn := func(owner calendar.Scope, e audit.Entry) func(*testing.T, Harness, namespace.Fence) error {
		return func(t *testing.T, h Harness, fence namespace.Fence) error {
			t.Helper()

			return h.Store.Create(t.Context(), sample("cal-new", owner), fence, e)
		}
	}
	assignTo := func(s calendar.Scope, id calendar.ID, e audit.Entry) func(*testing.T, Harness, namespace.Fence) error {
		return func(t *testing.T, h Harness, fence namespace.Fence) error {
			t.Helper()

			return h.Store.Assign(t.Context(), calendar.Assignment{Scope: s, Calendar: id}, fence, e)
		}
	}
	replaceIn := func(id calendar.ID) func(*testing.T, Harness, namespace.Fence) error {
		return func(t *testing.T, h Harness, fence namespace.Fence) error {
			t.Helper()

			hs := []calendar.Holiday{fromFeed(date(12, 25), "Christmas Day")}

			read := calendar.FeedRead{Feed: feedID, URL: feedURL, Holidays: hs, Status: goodStatus(1, "v9")}

			return h.Store.ReplaceFeedHolidays(t.Context(), id, read, fence)
		}
	}
	recordIn := func(id calendar.ID) func(*testing.T, Harness, namespace.Fence) error {
		return func(t *testing.T, h Harness, fence namespace.Fence) error {
			t.Helper()

			s := calendar.FeedStatus{ReadAt: now(), Holidays: 0, Skipped: 0, Error: calendar.FeedTimeout, ETag: ""}

			return h.Store.RecordFeedStatus(t.Context(), id, status(feedID, s), fence)
		}
	}

	return []fenceWrite{
		{"create", calendar.ErrInvalid, createIn(nsScope(shop), nsEntry()), createIn(groupScope(), serverEntry())},
		{
			"update", calendar.ErrNotFound,
			update("cal-berlin", nsScope(shop), nsEntry()), update("cal-team", groupScope(), serverEntry()),
		},
		{"delete", calendar.ErrNotFound, del("cal-berlin", nsEntry()), del("cal-team", serverEntry())},
		{
			"assign", calendar.ErrInvalid,
			assignTo(nsScope(shop), "cal-berlin", nsEntry()), assignTo(groupScope(), "cal-team", serverEntry()),
		},
		{"replace feed holidays", calendar.ErrNotFound, replaceIn("cal-berlin"), replaceIn("cal-team")},
		{"record feed status", calendar.ErrNotFound, recordIn("cal-berlin"), recordIn("cal-team")},
	}
}

// snapshot is what a refused write must leave as it was.
type snapshot struct {
	berlin, team calendar.Calendar
	resolved     []calendar.Resolved
	entries      []int
}

func takeSnapshot(t *testing.T, h Harness) snapshot {
	t.Helper()

	return snapshot{
		berlin:   get(t, h, shop, "cal-berlin"),
		team:     get(t, h, audit.ServerScope, "cal-team"),
		resolved: []calendar.Resolved{resolve(t, h, nsScope(shop)), resolve(t, h, groupScope())},
		entries:  []int{auditCount(t, h, shop), auditCount(t, h, lab), auditCount(t, h, audit.ServerScope)},
	}
}

func fenceFixture(t *testing.T, h Harness) fixture {
	t.Helper()

	f := setup(t, h)
	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))
	create(t, h, noFence(), sample("cal-team", groupScope()))

	return f
}

// fenceCases returns, for every write method, the cases of a zero fence,
// the fence of another namespace and a stale fence on a row of a
// namespace, and of a namespace fence on a row of the server scope.
func fenceCases() []testCase {
	kinds := []struct {
		name   string
		server bool
		fence  func(t *testing.T, h Harness, f fixture) namespace.Fence
		want   func(w fenceWrite) error
	}{
		{
			"a zero fence on a row of a namespace", false,
			func(*testing.T, Harness, fixture) namespace.Fence { return noFence() },
			func(w fenceWrite) error { return w.refused },
		},
		{
			"the fence of another namespace", false,
			func(_ *testing.T, _ Harness, f fixture) namespace.Fence { return f.lab },
			func(w fenceWrite) error { return w.refused },
		},
		{
			"a stale fence", false,
			func(t *testing.T, h Harness, f fixture) namespace.Fence {
				t.Helper()

				if _, err := h.Domain.BumpFence(t.Context(), f.shop); err != nil {
					t.Fatalf("BumpFence: %v", err)
				}

				return f.shop
			},
			func(fenceWrite) error { return namespace.ErrFenced },
		},
		{
			"a namespace fence on a row of the server scope", true,
			func(_ *testing.T, _ Harness, f fixture) namespace.Fence { return f.shop },
			func(w fenceWrite) error { return w.refused },
		},
	}

	writes := fenceWrites()
	cases := make([]testCase, 0, len(writes)*len(kinds))

	for _, w := range writes {
		for _, k := range kinds {
			cases = append(cases, testCase{
				name: w.name + " with " + k.name + " changes nothing",
				run: func(t *testing.T, h Harness) {
					t.Helper()

					f := fenceFixture(t, h)
					before := takeSnapshot(t, h)
					fence := k.fence(t, h, f)

					write := w.inShop
					if k.server {
						write = w.inServer
					}

					wantErr(t, w.name, write(t, h, fence), k.want(w))

					if after := takeSnapshot(t, h); !reflect.DeepEqual(before, after) {
						t.Fatalf("a refused %s changed the store:\nbefore %+v\nafter  %+v", w.name, before, after)
					}
				},
			})
		}
	}

	return cases
}

func testFeedURLChanged(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)
	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))
	replace(t, h, f.shop, "cal-berlin", fromFeed(date(12, 25), "Christmas Day"))

	// An update gives the feed a new URL while a read of the old URL runs.
	c := get(t, h, shop, "cal-berlin")
	c.Feeds[0].URL = otherURL

	if err := h.Store.Update(t.Context(), c, f.shop, nsEntry()); err != nil {
		t.Fatalf("Update: %v", err)
	}

	before := get(t, h, shop, "cal-berlin")

	old := calendar.FeedRead{
		Feed:     feedID,
		URL:      feedURL,
		Holidays: []calendar.Holiday{fromFeed(date(12, 26), "Boxing Day")},
		Status:   goodStatus(1, "v2"),
	}

	err := h.Store.ReplaceFeedHolidays(t.Context(), "cal-berlin", old, f.shop)
	wantErr(t, "ReplaceFeedHolidays of the old URL", err, calendar.ErrConflict)

	failed := calendar.FeedStatus{ReadAt: now(), Holidays: 0, Skipped: 0, Error: calendar.FeedTimeout, ETag: ""}

	err = h.Store.RecordFeedStatus(t.Context(), "cal-berlin", status(feedID, failed), f.shop)
	wantErr(t, "RecordFeedStatus of the old URL", err, calendar.ErrConflict)

	after := get(t, h, shop, "cal-berlin")
	if len(after.Holidays) != len(before.Holidays) || after.Feeds[0].Status != before.Feeds[0].Status {
		t.Fatalf("calendar = %+v, want no change from %+v", after, before)
	}
}

func testEntryOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	f := setup(t, h)
	other := entry(lab, nsAction)

	err := h.Store.Create(t.Context(), sample("cal-berlin", nsScope(shop)), f.shop, other)
	wantErr(t, "Create with an entry of lab", err, calendar.ErrInvalid)

	_, err = h.Store.Get(t.Context(), shop, "cal-berlin")
	wantErr(t, "Get after a refused create", err, calendar.ErrNotFound)

	create(t, h, f.shop, sample("cal-berlin", nsScope(shop)))

	c := get(t, h, shop, "cal-berlin")
	c.Name = "Changed"

	err = h.Store.Update(t.Context(), c, f.shop, other)
	wantErr(t, "Update with an entry of lab", err, calendar.ErrNotFound)

	if got := get(t, h, shop, "cal-berlin"); got.Name == "Changed" || got.Version != 1 {
		t.Fatalf("calendar = %+v, want no change", got)
	}

	if n := auditCount(t, h, lab); n != 0 {
		t.Fatalf("lab audit entries = %d, want 0", n)
	}

	if n := auditCount(t, h, shop); n != 1 {
		t.Fatalf("shop audit entries = %d, want 1 for the good create", n)
	}
}
