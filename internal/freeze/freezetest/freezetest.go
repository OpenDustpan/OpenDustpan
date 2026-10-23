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
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/freeze"
	"opendustpan.org/opendustpan/pkg/audit"
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
		{"the writes of a schedule never match a freeze of another namespace", testOtherNamespace},
		{"create freeze refuses the creator of a schedule", testCreateFreezeSubject},
		{"a write refuses an audit entry of another namespace", testEntryScope},
		{"one start in two namespaces makes two freezes", testSameStartTwoNamespaces},
		{"create freeze refuses an ID that exists", testCreateFreezeExists},
		{"get of a schedule of another namespace is ErrNotFound", testGetOtherNamespace},
		{"create freeze refuses an ID in the form of an occurrence", testCreateFreezeOccurrenceID},
		{"a create freeze with an occurrence ID never stops the replace", testCreateFreezeOccurrenceRace},
		{"delete scheduled after the ID is used in another namespace", testDeleteScheduledReused},
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
		At:            now(),
		Namespace:     shop,
		Subject:       "alice",
		IDPSubject:    "",
		TokenID:       "",
		Action:        action,
		Object:        shop.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "suite",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "req-suite",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
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

	got, err := h.Schedules.Get(t.Context(), shop, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Version != 1 || got.Kind != freeze.KindCalendar || !got.Calendar.DaysOff || got.Namespace != shop {
		t.Fatalf("Get = %+v, want the created schedule with version 1", got)
	}

	if _, err := h.Schedules.Get(t.Context(), shop, "fzs-none"); !errors.Is(err, freeze.ErrNotFound) {
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

	got, err := h.Schedules.Get(t.Context(), shop, id)
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

	got, err := h.Schedules.Get(t.Context(), shop, id)
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

	got, err := h.Schedules.Get(t.Context(), shop, id)
	if err != nil || got.Version != 1 || got.Name != s.Name {
		t.Fatalf("schedule after refused writes = %+v (%v), want it unchanged", got, err)
	}

	if _, err := h.Schedules.Get(t.Context(), shop, "fzs-new"); !errors.Is(err, freeze.ErrNotFound) {
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
		// foreign is the error of the fence of another namespace, when it
		// is not scope: DeleteScheduled with that fence deletes the
		// freezes of that namespace only, and there are none.
		foreign error
	}{
		{"Create", func(f namespace.Fence) error {
			return h.Schedules.Create(t.Context(), sample("fzs-new"), f, entry())
		}, freeze.ErrInvalid, freeze.ErrInvalid},
		{"Update", func(f namespace.Fence) error {
			u := s
			u.Name = "Changed"

			return h.Schedules.Update(t.Context(), u, f, entry())
		}, freeze.ErrNotFound, freeze.ErrNotFound},
		{"Delete", func(f namespace.Fence) error {
			return h.Schedules.Delete(t.Context(), id, f, entry())
		}, freeze.ErrNotFound, freeze.ErrNotFound},
		{"ReplaceScheduled", func(f namespace.Fence) error {
			return h.Freezes.ReplaceScheduled(t.Context(), s, nil, f)
		}, freeze.ErrNotFound, freeze.ErrNotFound},
		{"DeleteScheduled", func(f namespace.Fence) error {
			return h.Freezes.DeleteScheduled(t.Context(), "fzs-gone", f)
		}, freeze.ErrNotFound, nil},
	}

	fences := []struct {
		name    string
		fence   namespace.Fence
		stale   bool
		foreign bool
	}{
		{"zero fence", zero, false, false},
		{"fence of another namespace", foreign, false, true},
		{"stale fence", stale, true, false},
	}

	for _, w := range writes {
		for _, f := range fences {
			want := w.scope

			switch {
			case f.stale:
				want = namespace.ErrFenced
			case f.foreign:
				want = w.foreign
			default:
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
	if len(fs) != 1 || fs[0].ID != freeze.OccurrenceID(shop, id, occ(id, 24, 0).Start) ||
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

	fid := freeze.OccurrenceID(shop, id, o.Start)
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

// testOtherNamespace leaves the freezes of the schedule id in other, then
// creates a schedule with the free ID id in shop. The writes of the
// schedule in shop must not change the freezes in other, and the freezes
// in other must not stop an add in shop.
func testOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	foreign, left := leaveOtherFreezes(t, h)

	s := create(t, h, fence, id)

	// The started occurrence overlaps the started freeze in other, and the
	// plan has no occurrence for the freeze in other that did not start.
	replace(t, h, fence, s, occ(id, -1, 5))

	if fs := freezes(t, h); len(fs) != 1 || !same(fs[0], s.FreezeOf(occ(id, -1, 5))) {
		t.Fatalf("freezes in shop = %+v, want the started occurrence of shop", fs)
	}

	wantOther(t, h, left, "replace in shop")

	if err := h.Schedules.Delete(t.Context(), id, fence, entry()); err != nil {
		t.Fatalf("Delete in shop: %v", err)
	}

	if err := h.Freezes.DeleteScheduled(t.Context(), id, fence); err != nil {
		t.Fatalf("DeleteScheduled with the fence of shop: %v", err)
	}

	wantOther(t, h, left, "delete scheduled in shop")

	if err := h.Freezes.DeleteScheduled(t.Context(), id, foreign); err != nil {
		t.Fatalf("DeleteScheduled with the fence of other: %v", err)
	}

	wantOther(t, h, left[:1], "delete scheduled in other")
}

// leaveOtherFreezes creates the schedule id in other with a started freeze
// and a freeze that did not start, then deletes the schedule and leaves
// its freezes. It returns the fence of other and the freezes, the started
// one first.
func leaveOtherFreezes(t *testing.T, h Harness) (namespace.Fence, []job.Freeze) {
	t.Helper()

	foreign, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	o := sample(id)
	o.Namespace = other

	e := entry()
	e.Namespace, e.Object = other, other.String()

	if err := h.Schedules.Create(t.Context(), o, foreign, e); err != nil {
		t.Fatalf("Create in other: %v", err)
	}

	o.Version = 1
	replace(t, h, foreign, o, occ(id, -2, 6), occ(id, 24, 48))

	if err := h.Schedules.Delete(t.Context(), id, foreign, e); err != nil {
		t.Fatalf("Delete in other: %v", err)
	}

	left := otherFreezes(t, h)
	if len(left) != 2 {
		t.Fatalf("freezes in other = %+v, want 2", left)
	}

	return foreign, left
}

// wantOther checks that the freezes of other are want after step.
func wantOther(t *testing.T, h Harness, want []job.Freeze, step string) {
	t.Helper()

	if got := otherFreezes(t, h); !slices.EqualFunc(got, want, same) {
		t.Fatalf("freezes in other after %s = %+v, want %+v", step, got, want)
	}
}

func otherFreezes(t *testing.T, h Harness) []job.Freeze {
	t.Helper()

	fs, err := h.Freezes.ListFreezes(t.Context(), other, time.Time{})
	if err != nil {
		t.Fatalf("ListFreezes: %v", err)
	}

	return fs
}

func testCreateFreezeSubject(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	before := entries(t, h)

	f := job.Freeze{
		ID:        "frz-fake",
		Namespace: shop,
		Reason:    "Not a schedule",
		StartsAt:  now().Add(-time.Hour),
		EndsAt:    now().Add(time.Hour),
		CreatedBy: freeze.Subject(id),
		EndedBy:   "",
	}

	if err := h.Freezes.CreateFreeze(t.Context(), f, fence, entry()); !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("CreateFreeze as %s: err = %v, want job.ErrInvalid", f.CreatedBy, err)
	}

	if fs := freezes(t, h); len(fs) != 0 || entries(t, h) != before {
		t.Fatalf("after a refused create: %d freezes, %d entries; want none and %d", len(fs), entries(t, h), before)
	}
}

func testEntryScope(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	s := create(t, h, fence, id)

	manual := job.Freeze{
		ID:        "frz-manual",
		Namespace: shop,
		Reason:    "Incident",
		StartsAt:  now().Add(-time.Hour),
		EndsAt:    time.Time{},
		CreatedBy: "alice",
		EndedBy:   "",
	}
	if err := h.Freezes.CreateFreeze(t.Context(), manual, fence, entry()); err != nil {
		t.Fatalf("CreateFreeze: %v", err)
	}

	if _, err := h.Domain.CreateNamespace(t.Context(), other); err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	foreign := entry()
	foreign.Namespace = other

	before := entries(t, h)
	u := s
	u.Name = "Changed"
	f := manual
	f.ID = "frz-new"

	writes := []struct {
		name string
		err  error
		want error
	}{
		{"Create", h.Schedules.Create(t.Context(), sample("fzs-new"), fence, foreign), freeze.ErrInvalid},
		{"Update", h.Schedules.Update(t.Context(), u, fence, foreign), freeze.ErrNotFound},
		{"Delete", h.Schedules.Delete(t.Context(), id, fence, foreign), freeze.ErrNotFound},
		{"CreateFreeze", h.Freezes.CreateFreeze(t.Context(), f, fence, foreign), job.ErrInvalid},
		{"EndFreeze", h.Freezes.EndFreeze(t.Context(), manual.ID, "alice", fence, foreign), job.ErrNotFound},
	}

	for _, w := range writes {
		if !errors.Is(w.err, w.want) {
			t.Fatalf("%s with an entry of other: err = %v, want %v", w.name, w.err, w.want)
		}
	}

	wantUnchanged(t, h, s, manual, before)
}

// wantUnchanged checks that the schedule s and the freeze manual are the
// only rows, that shop has before audit entries, and other has none.
func wantUnchanged(t *testing.T, h Harness, s freeze.Schedule, manual job.Freeze, before int) {
	t.Helper()

	got, err := h.Schedules.Get(t.Context(), shop, id)
	if err != nil || got.Version != 1 || got.Name != s.Name {
		t.Fatalf("schedule after refused writes = %+v (%v), want it unchanged", got, err)
	}

	if _, err = h.Schedules.Get(t.Context(), shop, "fzs-new"); !errors.Is(err, freeze.ErrNotFound) {
		t.Fatalf("Get of a refused create: err = %v, want ErrNotFound", err)
	}

	if fs := freezes(t, h); len(fs) != 1 || !same(fs[0], manual) || entries(t, h) != before {
		t.Fatalf("after refused writes: freezes %+v, %d entries; want the manual freeze and %d entries",
			fs, entries(t, h), before)
	}

	es, err := h.Domain.AuditEntries(t.Context(), other)
	if err != nil || len(es) != 0 {
		t.Fatalf("audit entries of other = %d (%v), want none", len(es), err)
	}
}

// testSameStartTwoNamespaces leaves a freeze of the schedule id in other,
// then plans the same occurrence for the schedule id in shop.
func testSameStartTwoNamespaces(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	_, left := leaveOtherFreezes(t, h)

	s := create(t, h, fence, id)
	replace(t, h, fence, s, occ(id, -2, 6), occ(id, 24, 48))

	fs := freezes(t, h)
	if len(fs) != 2 || !same(fs[0], s.FreezeOf(occ(id, -2, 6))) || !same(fs[1], s.FreezeOf(occ(id, 24, 48))) {
		t.Fatalf("freezes in shop = %+v, want both occurrences of shop", fs)
	}

	if fs[0].ID == left[0].ID || fs[1].ID == left[1].ID {
		t.Fatalf("freezes in shop %+v share an ID with the freezes in other %+v", fs, left)
	}

	wantOther(t, h, left, "replace in shop")
}

func testCreateFreezeExists(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	foreign, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	f := job.Freeze{
		ID:        "frz-manual",
		Namespace: shop,
		Reason:    "Incident",
		StartsAt:  now().Add(-time.Hour),
		EndsAt:    time.Time{},
		CreatedBy: "alice",
		EndedBy:   "",
	}
	if err := h.Freezes.CreateFreeze(t.Context(), f, fence, entry()); err != nil {
		t.Fatalf("CreateFreeze: %v", err)
	}

	before := entries(t, h)

	again := f
	again.Reason = "Changed"

	if err := h.Freezes.CreateFreeze(t.Context(), again, fence, entry()); !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("CreateFreeze of an ID that exists: err = %v, want job.ErrInvalid", err)
	}

	moved := f
	moved.Namespace = other

	e := entry()
	e.Namespace, e.Object = other, other.String()

	if err := h.Freezes.CreateFreeze(t.Context(), moved, foreign, e); !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("CreateFreeze of an ID that exists in another namespace: err = %v, want job.ErrInvalid", err)
	}

	if fs := freezes(t, h); len(fs) != 1 || !same(fs[0], f) || entries(t, h) != before {
		t.Fatalf("after refused creates: freezes %+v, %d entries; want the first freeze and %d entries",
			fs, entries(t, h), before)
	}

	if fs := otherFreezes(t, h); len(fs) != 0 {
		t.Fatalf("freezes in other = %+v, want none", fs)
	}
}

func testGetOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	create(t, h, fence, id)

	if _, err := h.Domain.CreateNamespace(t.Context(), other); err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	if _, err := h.Schedules.Get(t.Context(), other, id); !errors.Is(err, freeze.ErrNotFound) {
		t.Fatalf("Get of %s in %s: err = %v, want ErrNotFound", id, other, err)
	}

	if _, err := h.Schedules.Get(t.Context(), "", id); !errors.Is(err, freeze.ErrNotFound) {
		t.Fatalf("Get of %s with no namespace: err = %v, want ErrNotFound", id, err)
	}
}

// occurrenceFreeze returns a freeze of a person in ns whose ID is the ID
// of the occurrence o of the schedule id in shop.
func occurrenceFreeze(ns namespace.Name, o freeze.Occurrence) job.Freeze {
	return job.Freeze{
		ID:        freeze.OccurrenceID(shop, id, o.Start),
		Namespace: ns,
		Reason:    "Not a schedule",
		StartsAt:  o.Start,
		EndsAt:    o.End,
		CreatedBy: "mallory",
		EndedBy:   "",
	}
}

// otherEntry returns the audit entry of a write in other.
func otherEntry() audit.Entry {
	e := entry()
	e.Namespace, e.Object = other, other.String()

	return e
}

// testCreateFreezeOccurrenceID creates, in other and in shop, a freeze of
// a person with the ID of an occurrence of a schedule in shop, before the
// schedule plans it. Each create fails, and the replace in shop then
// writes the freeze of the schedule.
func testCreateFreezeOccurrenceID(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	foreign, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	o := occ(id, 24, 48)

	err = h.Freezes.CreateFreeze(t.Context(), occurrenceFreeze(other, o), foreign, otherEntry())
	if !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("CreateFreeze in %s with an occurrence ID of %s: err = %v, want job.ErrInvalid", other, shop, err)
	}

	err = h.Freezes.CreateFreeze(t.Context(), occurrenceFreeze(shop, o), fence, entry())
	if !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("CreateFreeze in %s with an occurrence ID: err = %v, want job.ErrInvalid", shop, err)
	}

	s := create(t, h, fence, id)
	replace(t, h, fence, s, o)

	if fs := freezes(t, h); len(fs) != 1 || !same(fs[0], s.FreezeOf(o)) {
		t.Fatalf("freezes in shop = %+v, want the freeze of the schedule", fs)
	}

	if fs := otherFreezes(t, h); len(fs) != 0 {
		t.Fatalf("freezes in other = %+v, want none", fs)
	}
}

// testCreateFreezeOccurrenceRace runs the replace of a schedule in shop
// and a create of a freeze in other with the occurrence ID at one time.
// In each order, the create fails and the freeze of the schedule exists.
func testCreateFreezeOccurrenceRace(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)

	foreign, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	s := create(t, h, fence, id)
	o := occ(id, 24, 48)

	var (
		wg                  sync.WaitGroup
		replaced, createErr error
	)

	start := make(chan struct{})

	wg.Go(func() {
		<-start

		replaced = h.Freezes.ReplaceScheduled(t.Context(), s, []freeze.Occurrence{o}, fence)
	})
	wg.Go(func() {
		<-start

		createErr = h.Freezes.CreateFreeze(t.Context(), occurrenceFreeze(other, o), foreign, otherEntry())
	})

	close(start)
	wg.Wait()

	if replaced != nil || !errors.Is(createErr, job.ErrInvalid) {
		t.Fatalf("ReplaceScheduled = %v, CreateFreeze = %v; want nil and job.ErrInvalid", replaced, createErr)
	}

	if fs := freezes(t, h); len(fs) != 1 || !same(fs[0], s.FreezeOf(o)) {
		t.Fatalf("freezes in shop = %+v, want the freeze of the schedule", fs)
	}

	if fs := otherFreezes(t, h); len(fs) != 0 {
		t.Fatalf("freezes in other = %+v, want none", fs)
	}
}

// testDeleteScheduledReused deletes the schedule id in shop, and other
// creates a schedule with the free ID id before the delete of the freezes
// in shop. The delete of the freezes in shop still removes each freeze
// that did not start, and refuses in other, where the schedule exists.
func testDeleteScheduledReused(t *testing.T, h Harness) {
	t.Helper()

	fence := setup(t, h)
	s := create(t, h, fence, id)
	replace(t, h, fence, s, occ(id, -2, 6), occ(id, 24, 48))

	if err := h.Schedules.Delete(t.Context(), id, fence, entry()); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	foreign, err := h.Domain.CreateNamespace(t.Context(), other)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	o := sample(id)
	o.Namespace = other

	if err := h.Schedules.Create(t.Context(), o, foreign, otherEntry()); err != nil {
		t.Fatalf("Create in other: %v", err)
	}

	if _, err := h.Schedules.Get(t.Context(), shop, id); !errors.Is(err, freeze.ErrNotFound) {
		t.Fatalf("Get of %s in %s: err = %v, want ErrNotFound", id, shop, err)
	}

	if err := h.Freezes.DeleteScheduled(t.Context(), id, fence); err != nil {
		t.Fatalf("DeleteScheduled in shop: %v", err)
	}

	if fs := freezes(t, h); len(fs) != 1 || !slices.Equal(ends(fs), []int{6}) {
		t.Fatalf("freezes in shop = %+v, want the started freeze only", fs)
	}

	if err := h.Freezes.DeleteScheduled(t.Context(), id, foreign); !errors.Is(err, freeze.ErrExists) {
		t.Fatalf("DeleteScheduled in other: err = %v, want ErrExists", err)
	}
}
