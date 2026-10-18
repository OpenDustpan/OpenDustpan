// Package freezetest is the conformance suite for freeze.Store and
// freeze.Sink implementations. Every store adapter runs it:
//
//	func TestFreezeConformance(t *testing.T) {
//		t.Parallel()
//		freezetest.Run(t, func(t *testing.T) freezetest.Harness { return newHarness(t) })
//	}
//
// A reference adapter in memory, in the tests of this package, runs the
// suite, so each case runs before the first SQL adapter. The suite covers
// the writes of the schedules. The suite of the job model covers the
// methods of job.FreezeStore.
package freezetest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/freeze"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Harness is one fresh, empty database for one case. Schedules and Freezes
// share the database.
type Harness struct {
	Schedules freeze.Store
	Freezes   freeze.Sink
	Domain    Domain
}

// Domain makes the rows that other ports own, reads the audit entries and
// sets the clock of the store.
type Domain interface {
	// CreateNamespace creates the namespace name and returns its fence.
	CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error)
	// BumpFence increments the failover version of the namespace of fence.
	BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error)
	// AuditEntries returns the audit entries of scope, in append order.
	AuditEntries(ctx context.Context, scope namespace.Name) ([]audit.Entry, error)
	// SetNow sets the clock of the store, which decides whether a freeze
	// started and which time EndFreeze records.
	SetNow(t time.Time)
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
		{"update and delete refuse unknown and invalid schedules", testUpdateDeleteErrors},
		{"each write refuses a zero, foreign or stale fence", testFences},
		{"list returns the schedules of one namespace", testList},
		{"only the writes of a person append audit entries", testAuditCounts},
		{"replace adds the occurrences once, as valid freezes", testReplaceIdempotent},
		{"replace changes and deletes freezes that did not start", testReplaceFuture},
		{"replace never moves the end of a started freeze earlier", testReplaceKeepsStarted},
		{"replace moves the end of a started freeze later", testReplaceGrowsStarted},
		{"replace adds a started occurrence that has no freeze", testReplaceAddsStarted},
		{"a freeze that a person ended does not come back", testReplaceKeepsEnded},
		{"replace leaves manual freezes and other schedules", testReplaceOthers},
		{"replace checks the schedule and its occurrences", testReplaceChecks},
		{"delete scheduled removes the freezes of a gone schedule that did not start", testDeleteScheduled},
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
	other  = namespace.Name("other")
	action = "namespace:manage-freezes"
	id     = "fzs-weekend"
	day    = 24 * time.Hour
)

func now() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }

// entry returns a valid audit entry of the kind change. The suite keeps
// its own helper, so that it depends on no adapter.
func entry() audit.Entry {
	return audit.Entry{
		At:           now(),
		Namespace:    shop,
		Subject:      "alice",
		IDPSubject:   "",
		TokenID:      "",
		Action:       action,
		Object:       shop.String(),
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

func setup(t *testing.T, h Harness) namespace.Fence {
	t.Helper()

	h.Domain.SetNow(now())

	fence, err := h.Domain.CreateNamespace(t.Context(), shop)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	return fence
}

func sample(sid string) freeze.Schedule {
	return freeze.Schedule{
		ID:        sid,
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

// create stores sample(sid) and returns it with its stored version.
func create(t *testing.T, h Harness, fence namespace.Fence, sid string) freeze.Schedule {
	t.Helper()

	s := sample(sid)
	if err := h.Schedules.Create(t.Context(), s, fence, entry()); err != nil {
		t.Fatalf("Create %s: %v", sid, err)
	}

	s.Version = 1

	return s
}

// occ returns an occurrence of the schedule sid, in hours from now().
func occ(sid string, startHours, endHours int) freeze.Occurrence {
	return freeze.Occurrence{
		Schedule: sid,
		Start:    now().Add(time.Duration(startHours) * time.Hour),
		End:      now().Add(time.Duration(endHours) * time.Hour),
	}
}

func freezes(t *testing.T, h Harness) []job.Freeze {
	t.Helper()

	fs, err := h.Freezes.ListFreezes(t.Context(), shop, time.Time{})
	if err != nil {
		t.Fatalf("ListFreezes: %v", err)
	}

	return fs
}

func entries(t *testing.T, h Harness) int {
	t.Helper()

	es, err := h.Domain.AuditEntries(t.Context(), shop)
	if err != nil {
		t.Fatalf("AuditEntries: %v", err)
	}

	return len(es)
}

func replace(t *testing.T, h Harness, fence namespace.Fence, s freeze.Schedule, occs ...freeze.Occurrence) {
	t.Helper()

	if err := h.Freezes.ReplaceScheduled(t.Context(), s, occs, fence); err != nil {
		t.Fatalf("ReplaceScheduled: %v", err)
	}
}

// ends returns the end of each freeze, in hours from now(), in the order
// of fs.
func ends(fs []job.Freeze) []int {
	out := make([]int, 0, len(fs))
	for i := range fs {
		out = append(out, int(fs[i].EndsAt.Sub(now())/time.Hour))
	}

	return out
}

// same reports whether two freezes have the same fields, with times that
// are the same instant.
func same(a, b job.Freeze) bool {
	return a.ID == b.ID && a.Namespace == b.Namespace && a.Reason == b.Reason &&
		a.StartsAt.Equal(b.StartsAt) && a.EndsAt.Equal(b.EndsAt) &&
		a.CreatedBy == b.CreatedBy && a.EndedBy == b.EndedBy
}

func testCreateGet(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, id)

	got, err := h.Schedules.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Version != 1 || got.Kind != freeze.KindCalendar || !got.Calendar.DaysOff || got.Namespace != shop {
		t.Fatalf("Get = %+v, want the created schedule with version 1", got)
	}

	if _, err := h.Schedules.Get(t.Context(), "fzs-none"); !errors.Is(err, freeze.ErrNotFound) {
		t.Fatalf("Get unknown: err = %v, want ErrNotFound", err)
	}
}

func testCreateExists(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, id)

	err := h.Schedules.Create(t.Context(), sample(id), fence, entry())
	if !errors.Is(err, freeze.ErrExists) {
		t.Fatalf("second Create: err = %v, want ErrExists", err)
	}

	bad := sample("fzs-bad")
	bad.Reason = ""

	if err := h.Schedules.Create(t.Context(), bad, fence, entry()); !errors.Is(err, freeze.ErrInvalid) {
		t.Fatalf("Create with no reason: err = %v, want ErrInvalid", err)
	}
}

func testUpdateConflict(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	s := create(t, h, fence, id)

	s.Name = "Weekends"
	if err := h.Schedules.Update(t.Context(), s, fence, entry()); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := h.Schedules.Get(t.Context(), id)
	if err != nil || got.Version != 2 || got.Name != "Weekends" {
		t.Fatalf("Get after Update = %+v (%v), want version 2", got, err)
	}

	err = h.Schedules.Update(t.Context(), s, fence, entry())
	if !errors.Is(err, freeze.ErrConflict) {
		t.Fatalf("stale Update: err = %v, want ErrConflict", err)
	}
}

func testUpdateDeleteErrors(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	s := create(t, h, fence, id)

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"update of an unknown ID", func() error {
			u := sample("fzs-none")
			u.Version = 1

			return h.Schedules.Update(t.Context(), u, fence, entry())
		}, freeze.ErrNotFound},
		{"update that fails Validate", func() error {
			u := s
			u.Kind = "monthly"

			return h.Schedules.Update(t.Context(), u, fence, entry())
		}, freeze.ErrInvalid},
		{"update that moves the schedule to another namespace", func() error {
			u := s
			u.Namespace = other

			return h.Schedules.Update(t.Context(), u, fence, entry())
		}, freeze.ErrInvalid},
		{"delete of an unknown ID", func() error {
			return h.Schedules.Delete(t.Context(), "fzs-none", fence, entry())
		}, freeze.ErrNotFound},
	}

	for _, tc := range cases {
		if err := tc.call(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	got, err := h.Schedules.Get(t.Context(), id)
	if err != nil || got.Version != 1 || got.Namespace != shop || entries(t, h) != 1 {
		t.Fatalf("after refused writes: %+v (%v), %d entries; want the schedule unchanged and 1 entry",
			got, err, entries(t, h))
	}
}

// testFences runs each write method with a zero fence, the fence of
// another namespace and a stale fence, and checks that nothing changes.
func testFences(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	s := create(t, h, fence, id)
	replace(t, h, fence, s, occ(id, 24, 48))

	gone := create(t, h, fence, "fzs-gone")
	replace(t, h, fence, gone, occ("fzs-gone", 72, 96))

	if err := h.Schedules.Delete(t.Context(), "fzs-gone", fence, entry()); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	foreign, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	stale := fence

	fresh, err := h.Domain.BumpFence(t.Context(), fence)
	if err != nil {
		t.Fatalf("BumpFence: %v", err)
	}

	before := entries(t, h)

	refuseFences(t, h, s, foreign, stale)

	got, err := h.Schedules.Get(t.Context(), id)
	if err != nil || got.Version != 1 || got.Name != s.Name {
		t.Fatalf("schedule after refused writes = %+v (%v), want it unchanged", got, err)
	}

	if _, err := h.Schedules.Get(t.Context(), "fzs-new"); !errors.Is(err, freeze.ErrNotFound) {
		t.Fatalf("Get of a refused create: err = %v, want ErrNotFound", err)
	}

	if fs := freezes(t, h); len(fs) != 2 || entries(t, h) != before {
		t.Fatalf("after refused writes: %d freezes, %d entries; want 2 freezes and %d entries",
			len(fs), entries(t, h), before)
	}

	// The fresh fence passes, so each refusal above came from the fence.
	if err := h.Freezes.DeleteScheduled(t.Context(), "fzs-gone", fresh); err != nil {
		t.Fatalf("DeleteScheduled with the fresh fence: %v", err)
	}
}

// refuseFences calls each write method with a zero fence, the fence
// foreign of another namespace and the fence stale, and checks the error.
func refuseFences(t *testing.T, h Harness, s freeze.Schedule, foreign, stale namespace.Fence) {
	t.Helper()

	var zero namespace.Fence

	writes := []struct {
		name string
		call func(f namespace.Fence) error
		// scope is the error of a zero fence or the fence of another
		// namespace.
		scope error
	}{
		{"Create", func(f namespace.Fence) error {
			return h.Schedules.Create(t.Context(), sample("fzs-new"), f, entry())
		}, freeze.ErrInvalid},
		{"Update", func(f namespace.Fence) error {
			u := s
			u.Name = "Changed"

			return h.Schedules.Update(t.Context(), u, f, entry())
		}, freeze.ErrNotFound},
		{"Delete", func(f namespace.Fence) error {
			return h.Schedules.Delete(t.Context(), id, f, entry())
		}, freeze.ErrNotFound},
		{"ReplaceScheduled", func(f namespace.Fence) error {
			return h.Freezes.ReplaceScheduled(t.Context(), s, nil, f)
		}, freeze.ErrNotFound},
		{"DeleteScheduled", func(f namespace.Fence) error {
			return h.Freezes.DeleteScheduled(t.Context(), "fzs-gone", f)
		}, freeze.ErrNotFound},
	}

	fences := []struct {
		name  string
		fence namespace.Fence
		stale bool
	}{
		{"zero fence", zero, false},
		{"fence of another namespace", foreign, false},
		{"stale fence", stale, true},
	}

	for _, w := range writes {
		for _, f := range fences {
			want := w.scope
			if f.stale {
				want = namespace.ErrFenced
			}

			if err := w.call(f.fence); !errors.Is(err, want) {
				t.Fatalf("%s with a %s: err = %v, want %v", w.name, f.name, err, want)
			}
		}
	}
}

func testList(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	for _, sid := range []string{"fzs-b", "fzs-a"} {
		create(t, h, fence, sid)
	}

	got, err := h.Schedules.List(t.Context(), shop)
	if err != nil || len(got) != 2 || got[0].ID != "fzs-a" {
		t.Fatalf("List = %v (%v), want fzs-a then fzs-b", got, err)
	}

	got, err = h.Schedules.List(t.Context(), other)
	if err != nil || len(got) != 0 {
		t.Fatalf("List of another namespace = %v (%v), want none", got, err)
	}
}

func testAuditCounts(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	s := create(t, h, fence, id)

	replace(t, h, fence, s, occ(id, 24, 48))

	if n := entries(t, h); n != 1 {
		t.Fatalf("after Create and ReplaceScheduled: %d entries, want 1", n)
	}

	s.Name = "Weekends"
	if err := h.Schedules.Update(t.Context(), s, fence, entry()); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if err := h.Schedules.Delete(t.Context(), id, fence, entry()); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := h.Freezes.DeleteScheduled(t.Context(), id, fence); err != nil {
		t.Fatalf("DeleteScheduled: %v", err)
	}

	if n := entries(t, h); n != 3 {
		t.Fatalf("after Update, Delete and DeleteScheduled: %d entries, want 3", n)
	}
}

func testReplaceIdempotent(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	s := create(t, h, fence, id)

	replace(t, h, fence, s, occ(id, 24, 48), occ(id, 192, 216))
	replace(t, h, fence, s, occ(id, 24, 48), occ(id, 192, 216))

	fs := freezes(t, h)
	if len(fs) != 2 {
		t.Fatalf("freezes = %+v, want two", fs)
	}

	for i, o := range []freeze.Occurrence{occ(id, 24, 48), occ(id, 192, 216)} {
		f := fs[i]
		if !same(f, s.FreezeOf(o)) {
			t.Fatalf("freeze %d = %+v, want %+v", i, f, s.FreezeOf(o))
		}

		if err := f.Validate(); err != nil {
			t.Fatalf("freeze %d fails job.Freeze.Validate: %v", i, err)
		}

		if sid, ok := freeze.ScheduleOf(f); !ok || sid != id {
			t.Fatalf("ScheduleOf(freeze %d) = %q, %v; want %q", i, sid, ok, id)
		}
	}
}

func testReplaceFuture(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	s := create(t, h, fence, id)
	replace(t, h, fence, s, occ(id, 24, 48), occ(id, 192, 216))

	s.Reason = "Release week"
	if err := h.Schedules.Update(t.Context(), s, fence, entry()); err != nil {
		t.Fatalf("Update: %v", err)
	}

	s.Version++

	replace(t, h, fence, s, occ(id, 24, 30))

	fs := freezes(t, h)
	if len(fs) != 1 || !slices.Equal(ends(fs), []int{30}) || fs[0].Reason != "Release week" {
		t.Fatalf("freezes = %+v, want one with the new end and the new reason", fs)
	}
}

func testReplaceKeepsStarted(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	s := create(t, h, fence, id)
	replace(t, h, fence, s, occ(id, 24, 72))

	h.Domain.SetNow(now().Add(48 * time.Hour))

	// A shorter occurrence with the same ID, then none at all.
	replace(t, h, fence, s, occ(id, 24, 60))
	replace(t, h, fence, s)

	fs := freezes(t, h)
	if len(fs) != 1 || !slices.Equal(ends(fs), []int{72}) || fs[0].Reason != s.Reason {
		t.Fatalf("freezes = %+v, want the started freeze unchanged", fs)
	}
}

func testReplaceGrowsStarted(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	s := create(t, h, fence, id)
	replace(t, h, fence, s, occ(id, 24, 48))

	// A holiday that a feed adds after the start makes the occurrence
	// longer: the freeze keeps its ID and gets the later end.
	h.Domain.SetNow(now().Add(30 * time.Hour))
	replace(t, h, fence, s, occ(id, 24, 24+5*24))

	fs := freezes(t, h)
	if len(fs) != 1 || fs[0].ID != freeze.OccurrenceID(id, occ(id, 24, 0).Start) ||
		!slices.Equal(ends(fs), []int{24 + 5*24}) {
		t.Fatalf("freezes = %+v, want one with the later end", fs)
	}
}

func testReplaceAddsStarted(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	s := create(t, h, fence, id)

	// A schedule created during an occurrence adds it at once.
	replace(t, h, fence, s, occ(id, -2, 6), occ(id, 24, 48))

	fs := freezes(t, h)
	if len(fs) != 2 || !slices.Equal(ends(fs), []int{6, 48}) {
		t.Fatalf("freezes = %+v, want the started and the next occurrence", fs)
	}
}

func testReplaceKeepsEnded(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	s := create(t, h, fence, id)
	o := occ(id, 24, 72)
	replace(t, h, fence, s, o)

	h.Domain.SetNow(now().Add(36 * time.Hour))

	fid := freeze.OccurrenceID(id, o.Start)
	if err := h.Freezes.EndFreeze(t.Context(), fid, "bo", fence, entry()); err != nil {
		t.Fatalf("EndFreeze: %v", err)
	}

	h.Domain.SetNow(now().Add(40 * time.Hour))

	cases := []struct {
		name string
		occs []freeze.Occurrence
	}{
		{"the same occurrence", []freeze.Occurrence{o}},
		{"the same occurrence, longer", []freeze.Occurrence{occ(id, 24, 96)}},
		{"an earlier start, so another ID", []freeze.Occurrence{occ(id, 23, 72)}},
		{"a later start before the end, so another ID", []freeze.Occurrence{occ(id, 30, 72)}},
	}

	for _, tc := range cases {
		replace(t, h, fence, s, tc.occs...)

		fs := freezes(t, h)
		if len(fs) != 1 || fs[0].ID != fid || fs[0].EndedBy != "bo" || !slices.Equal(ends(fs), []int{36}) {
			t.Fatalf("%s: freezes = %+v, want the ended freeze only", tc.name, fs)
		}
	}
}

func testReplaceOthers(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	s := create(t, h, fence, id)
	b := create(t, h, fence, "fzs-nights")

	manual := job.Freeze{
		ID:        "frz-manual",
		Namespace: shop,
		Reason:    "Incident",
		StartsAt:  now().Add(24 * time.Hour),
		EndsAt:    now().Add(48 * time.Hour),
		CreatedBy: "alice",
		EndedBy:   "",
	}
	if err := h.Freezes.CreateFreeze(t.Context(), manual, fence, entry()); err != nil {
		t.Fatalf("CreateFreeze: %v", err)
	}

	replace(t, h, fence, s, occ(id, 24, 48))
	replace(t, h, fence, b, occ("fzs-nights", 30, 40))

	// The plan of s now has no occurrence.
	replace(t, h, fence, s)

	fs := freezes(t, h)
	if len(fs) != 2 || fs[0].ID != "frz-manual" || !same(fs[1], b.FreezeOf(occ("fzs-nights", 30, 40))) {
		t.Fatalf("freezes = %+v, want the manual freeze and the freeze of the other schedule", fs)
	}

	if _, ok := freeze.ScheduleOf(fs[0]); ok {
		t.Fatal("ScheduleOf(manual freeze) = true, want false")
	}
}

func testReplaceChecks(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	s := create(t, h, fence, id)

	newer := s
	newer.Name = "Weekends"

	if err := h.Schedules.Update(t.Context(), newer, fence, entry()); err != nil {
		t.Fatalf("Update: %v", err)
	}

	newer.Version++

	gone := sample("fzs-none")
	gone.Version = 1

	cases := []struct {
		name string
		s    freeze.Schedule
		occs []freeze.Occurrence
		want error
	}{
		{"an older version", s, []freeze.Occurrence{occ(id, 24, 48)}, freeze.ErrConflict},
		{"an unknown schedule", gone, []freeze.Occurrence{occ("fzs-none", 24, 48)}, freeze.ErrNotFound},
		{"an occurrence of another schedule", newer, []freeze.Occurrence{occ("fzs-other", 24, 48)}, freeze.ErrInvalid},
		{"an end before the start", newer, []freeze.Occurrence{occ(id, 48, 24)}, freeze.ErrInvalid},
		{
			"one valid and one invalid occurrence",
			newer,
			[]freeze.Occurrence{occ(id, 24, 48), occ(id, 72, 72)},
			freeze.ErrInvalid,
		},
	}

	for i := range cases {
		tc := &cases[i]
		if err := h.Freezes.ReplaceScheduled(t.Context(), tc.s, tc.occs, fence); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	if fs := freezes(t, h); len(fs) != 0 {
		t.Fatalf("freezes after refused replaces = %+v, want none", fs)
	}
}

func testDeleteScheduled(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	s := create(t, h, fence, id)
	replace(t, h, fence, s, occ(id, -2, 6), occ(id, 24, 48), occ(id, 72, 96))

	if err := h.Freezes.DeleteScheduled(t.Context(), id, fence); !errors.Is(err, freeze.ErrExists) {
		t.Fatalf("DeleteScheduled of a schedule that exists: err = %v, want ErrExists", err)
	}

	if err := h.Schedules.Delete(t.Context(), id, fence, entry()); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	for range 2 {
		if err := h.Freezes.DeleteScheduled(t.Context(), id, fence); err != nil {
			t.Fatalf("DeleteScheduled: %v", err)
		}
	}

	fs := freezes(t, h)
	if len(fs) != 1 || !slices.Equal(ends(fs), []int{6}) {
		t.Fatalf("freezes = %+v, want the started freeze only", fs)
	}

	if err := h.Freezes.DeleteScheduled(t.Context(), "fzs-never", fence); err != nil {
		t.Fatalf("DeleteScheduled of a schedule with no freeze: %v", err)
	}
}
