// Package sourcetest is the conformance suite of the source.Filters port. An
// adapter runs it with Run and a Factory that gives one fresh, empty set of
// ports for each case.
package sourcetest

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/source"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Values of the cases.
const (
	// filterAction is the action of the audit entry that the cases write. The
	// store keeps the entry that the caller gives.
	filterAction = "namespace:manage-runners"
	// filterKey is the key of the one change of the audit entry.
	filterKey = "sourceFilter"
	// filterAtMs is the time of the audit entry, in Unix milliseconds.
	filterAtMs = 1_790_000_000_000
	// acme is the namespace of most cases.
	acme namespace.Name = "acme"
	// globex is the second namespace of the cases.
	globex namespace.Name = "globex"
	// mainBranch is a protected branch of the cases.
	mainBranch = "main"
	// invalidOld is the prefix of the Old value of a change when the replaced
	// value was not a filter.
	invalidOld = "invalid:"
)

// Harness is one fresh, empty set of ports for one subtest.
type Harness struct {
	// Filters keeps the source filter of each namespace.
	Filters source.Filters
	// Namespace makes the namespace name and returns its current fence.
	Namespace func(t *testing.T, name string) namespace.Fence
	// Audited returns the audit entries of the namespace, oldest first. It is
	// required: the audit case fails when it is nil.
	Audited func(t *testing.T, ns string) []audit.Entry
	// FilterRaw is an optional hook: it writes raw as the stored value of the
	// filter of the namespace, with no check of the store, as a row of
	// another version or a restore could hold it. A case that needs it skips
	// when it is nil.
	FilterRaw func(t *testing.T, ns namespace.Name, raw string)
}

// Factory returns the Harness of one subtest.
type Factory func(t *testing.T) Harness

// suiteCase is one behavior of the port.
type suiteCase struct {
	test func(t *testing.T, h Harness)
	name string
}

// Run runs every case of the suite.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	for _, tc := range allCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.test(t, newHarness(t))
		})
	}
}

// Names returns the names of the cases of the suite, in the order of Run.
// Each call returns a new slice.
func Names() []string {
	cases := allCases()
	names := make([]string, 0, len(cases))

	for _, tc := range cases {
		names = append(names, tc.name)
	}

	return names
}

// allCases returns the cases of the suite. Each call returns a new slice.
func allCases() []suiteCase {
	return []suiteCase{
		{testDefault, "filters: the filter is empty for a namespace that never set it"},
		{testRoundTrip, "filters: set filter round trips each field and replaces the filter"},
		{testInvalidFilter, "filters: set filter refuses an invalid filter and changes nothing"},
		{testStoredInvalid, "filters: a stored value that is not a filter gives ErrInvalid"},
		{testFence, "filters: set filter refuses a stale fence and a fence of another namespace"},
		{testUnknownNamespace, "filters: the filter of an unknown namespace is not found"},
		{testIsolated, "filters: the filter of one namespace does not change another"},
		{testAudit, "filters: set filter appends one audit entry with the old value in its transaction"},
	}
}

// entry returns a valid audit entry of a change of the filter in the
// namespace of fence, with one change that has no Old value and the text of f
// as its New value.
func entry(t *testing.T, fence namespace.Fence, f source.Filter) audit.Entry {
	t.Helper()

	text, err := source.EncodeFilter(f)
	if err != nil {
		// A case that writes an invalid filter gives an entry of the empty
		// filter: the port refuses the filter before it reads the entry.
		text = ""
	}

	return EntryForFilter(fence, text)
}

// EntryForFilter returns a valid audit entry of a change of the filter in the
// namespace of fence, with one change that has no Old value and text as its
// New value. Tests of an adapter use it to call SetFilter.
func EntryForFilter(fence namespace.Fence, text string) audit.Entry {
	return audit.Entry{
		At:            time.UnixMilli(filterAtMs).UTC(),
		Namespace:     fence.Namespace,
		Subject:       "tester",
		IDPSubject:    "",
		TokenID:       "",
		Action:        filterAction,
		Object:        fence.Namespace.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       []audit.Change{{Key: filterKey, Old: nil, New: text, Layer: "", TakesEffect: ""}},
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		PrincipalKind: "",
		JobID:         "",
		Reason:        "",
	}
}

// full returns a valid filter that sets every field.
func full() source.Filter {
	return source.Filter{
		Submodules: []source.URLPattern{
			"https://git.example.com/platform/*",
			"https://git.example.com/acme/vpc",
		},
		LargeFiles:        []source.URLPattern{"https://git.example.com/acme/infra"},
		FailOnDenied:      true,
		ProtectedBranches: []string{mainBranch, "release/1.2"},
	}
}

// wantFilter fails the test unless the filter of ns is want. It compares the
// canonical text, so a nil list and an empty list are one value.
func wantFilter(t *testing.T, h Harness, ns namespace.Name, want source.Filter) {
	t.Helper()

	got, err := h.Filters.Filter(t.Context(), ns)
	if err != nil {
		t.Fatalf("Filter(%s): %v", ns, err)
	}

	gotText, err := source.EncodeFilter(got)
	if err != nil {
		t.Fatalf("EncodeFilter of the filter of %s: %v", ns, err)
	}

	wantText, err := source.EncodeFilter(want)
	if err != nil {
		t.Fatalf("EncodeFilter of the wanted filter: %v", err)
	}

	if gotText != wantText {
		t.Fatalf("Filter(%s) = %s, want %s", ns, gotText, wantText)
	}
}

// setFilter stores f as the filter of ns with the fence, and fails the test
// on an error.
func setFilter(t *testing.T, h Harness, fence namespace.Fence, f source.Filter) {
	t.Helper()

	if err := h.Filters.SetFilter(t.Context(), fence.Namespace, f, fence, entry(t, fence, f)); err != nil {
		t.Fatalf("SetFilter(%s): %v", fence.Namespace, err)
	}
}

// emptyFilter returns the empty filter.
func emptyFilter() source.Filter {
	return source.Filter{Submodules: nil, LargeFiles: nil, FailOnDenied: false, ProtectedBranches: nil}
}

// testDefault proves that a new namespace has the empty filter: a snapshot
// holds no submodule and no Git LFS object until a person sets the filter.
func testDefault(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, acme.String())
	wantFilter(t, h, acme, emptyFilter())

	got, err := h.Filters.Filter(t.Context(), acme)
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}

	if got.FailOnDenied || len(got.Submodules)+len(got.LargeFiles)+len(got.ProtectedBranches) != 0 {
		t.Fatalf("the default filter = %+v, want the empty filter", got)
	}
}

// testRoundTrip proves that each field round trips, that a new filter replaces
// the old one as a whole, and that the empty filter is a valid change.
func testRoundTrip(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, acme.String())

	setFilter(t, h, fence, full())
	wantFilter(t, h, acme, full())

	// The order of a list is kept.
	got, err := h.Filters.Filter(t.Context(), acme)
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}

	if !slices.Equal(got.ProtectedBranches, []string{mainBranch, "release/1.2"}) {
		t.Fatalf("ProtectedBranches = %q, want the order that was set", got.ProtectedBranches)
	}

	other := source.Filter{
		Submodules: nil, LargeFiles: []source.URLPattern{"https://git.example.com/x/y"}, FailOnDenied: false,
		ProtectedBranches: []string{"trunk"},
	}

	setFilter(t, h, fence, other)
	wantFilter(t, h, acme, other)

	setFilter(t, h, fence, emptyFilter())
	wantFilter(t, h, acme, emptyFilter())

	// A list at its limit is valid.
	limit := emptyFilter()
	for i := range source.MaxPatterns {
		limit.Submodules = append(limit.Submodules, source.URLPattern("https://git.example.com/m"+strconv.Itoa(i)))
		limit.ProtectedBranches = append(limit.ProtectedBranches, "b"+strconv.Itoa(i))
	}

	setFilter(t, h, fence, limit)
	wantFilter(t, h, acme, limit)
}

// testInvalidFilter proves that a filter that fails Validate is refused with
// source.ErrInvalid and changes nothing.
func testInvalidFilter(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, acme.String())
	setFilter(t, h, fence, full())

	tooMany := emptyFilter()
	for i := range source.MaxPatterns + 1 {
		tooMany.LargeFiles = append(tooMany.LargeFiles, source.URLPattern("https://git.example.com/m"+strconv.Itoa(i)))
	}

	tooManyBranches := emptyFilter()
	for i := range source.MaxPatterns + 1 {
		tooManyBranches.ProtectedBranches = append(tooManyBranches.ProtectedBranches, "b"+strconv.Itoa(i))
	}

	with := func(change func(f *source.Filter)) source.Filter {
		f := emptyFilter()
		change(&f)

		return f
	}

	cases := []struct {
		name string
		f    source.Filter
	}{
		{"more than 64 patterns", tooMany},
		{"more than 64 branches", tooManyBranches},
		{
			"a pattern with ftp",
			with(func(f *source.Filter) { f.Submodules = []source.URLPattern{"ftp://git.example.com/a"} }),
		},
		{
			"a pattern with a user",
			with(func(f *source.Filter) { f.LargeFiles = []source.URLPattern{"https://u@git.example.com/a"} }),
		},
		{"a glob branch", with(func(f *source.Filter) { f.ProtectedBranches = []string{"release/*"} })},
		{"a branch with a newline", with(func(f *source.Filter) { f.ProtectedBranches = []string{"main\nevil"} })},
		{"a branch twice", with(func(f *source.Filter) { f.ProtectedBranches = []string{mainBranch, mainBranch} })},
		{"an empty branch", with(func(f *source.Filter) { f.ProtectedBranches = []string{""} })},
		{"a branch above its limit", with(func(f *source.Filter) {
			f.ProtectedBranches = []string{strings.Repeat("a", source.MaxBranchName+1)}
		})},
	}

	for _, tc := range cases {
		err := h.Filters.SetFilter(t.Context(), acme, tc.f, fence, EntryForFilter(fence, "{}"))
		if !errors.Is(err, source.ErrInvalid) {
			t.Fatalf("%s: SetFilter = %v, want source.ErrInvalid", tc.name, err)
		}

		wantFilter(t, h, acme, full())
	}
}

// testStoredInvalid proves that a stored value that is not a filter, as a row
// of another version or a restore can hold, reads as source.ErrInvalid and
// never as a value, and that the error does not hold the value. A valid change
// replaces such a value, which repairs the row.
func testStoredInvalid(t *testing.T, h Harness) {
	t.Helper()

	if h.FilterRaw == nil {
		t.Skip("the adapter has no hook that writes a raw filter")
	}

	fence := h.Namespace(t, acme.String())

	for _, bad := range []string{
		"not json", "[]", "null", `{"extra":["canary-1"]}`, `{"submodules":["http://canary.example.com/a"]}`,
		`{"protectedBranches":["canary/*"]}`, `{} {}`, `{"failOnDenied":"canary"}`,
	} {
		h.FilterRaw(t, acme, bad)

		got, err := h.Filters.Filter(t.Context(), acme)
		if !errors.Is(err, source.ErrInvalid) {
			t.Fatalf("Filter of the stored value %q = %+v, %v, want source.ErrInvalid", bad, got, err)
		}

		if strings.Contains(err.Error(), "canary") {
			t.Fatalf("the error of the read holds the stored value: %v", err)
		}

		if got.FailOnDenied || len(got.Submodules)+len(got.LargeFiles)+len(got.ProtectedBranches) != 0 {
			t.Fatalf("Filter of the stored value %q = %+v with the error, want no value", bad, got)
		}

		setFilter(t, h, fence, full())
		wantFilter(t, h, acme, full())
	}
}

// testFence proves that a change with a stale fence, a fence of another
// namespace or a fence that the namespace never held is refused with
// namespace.ErrFenced and changes nothing.
func testFence(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, acme.String())
	other := h.Namespace(t, globex.String())

	for _, bad := range []namespace.Fence{
		{Namespace: acme, FailoverVersion: fence.FailoverVersion - 1},
		{Namespace: acme, FailoverVersion: fence.FailoverVersion + 1},
		other,
	} {
		err := h.Filters.SetFilter(t.Context(), acme, full(), bad, entry(t, bad, full()))
		if !errors.Is(err, namespace.ErrFenced) {
			t.Fatalf("SetFilter with the fence %+v = %v, want namespace.ErrFenced", bad, err)
		}

		wantFilter(t, h, acme, emptyFilter())
		wantFilter(t, h, globex, emptyFilter())
	}
}

// testUnknownNamespace proves that the read and the change of the filter of a
// namespace that does not exist answer namespace.ErrNotFound, and that a name
// that no namespace can have answers source.ErrInvalid.
func testUnknownNamespace(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, acme.String())

	for _, bad := range []namespace.Name{"", "../acme", "acme\n", "acme/prod", "acme%00", ".acme", "ac me"} {
		if _, err := h.Filters.Filter(t.Context(), bad); !errors.Is(err, source.ErrInvalid) {
			t.Fatalf("Filter(%q) = %v, want source.ErrInvalid", bad, err)
		}

		fence := namespace.Fence{Namespace: bad, FailoverVersion: 1}

		err := h.Filters.SetFilter(t.Context(), bad, full(), fence, EntryForFilter(fence, "{}"))
		if !errors.Is(err, source.ErrInvalid) {
			t.Fatalf("SetFilter(%q) = %v, want source.ErrInvalid", bad, err)
		}
	}

	wantFilter(t, h, acme, emptyFilter())

	if _, err := h.Filters.Filter(t.Context(), "nowhere"); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("Filter of an unknown namespace = %v, want namespace.ErrNotFound", err)
	}

	fence := namespace.Fence{Namespace: "nowhere", FailoverVersion: 1}

	err := h.Filters.SetFilter(t.Context(), "nowhere", full(), fence, entry(t, fence, full()))
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("SetFilter of an unknown namespace = %v, want namespace.ErrNotFound", err)
	}
}

// testIsolated proves that a change of the filter of one namespace leaves the
// filter of each other namespace as it is.
func testIsolated(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, acme.String())
	h.Namespace(t, globex.String())

	setFilter(t, h, fence, full())
	wantFilter(t, h, acme, full())
	wantFilter(t, h, globex, emptyFilter())
}

// testAudit proves that each change writes one audit entry in the namespace of
// the filter, that the store records the value that the change replaced as the
// Old value, and that a refused change writes none.
func testAudit(t *testing.T, h Harness) {
	t.Helper()

	// An adapter that cannot show its audit entries cannot prove that a change
	// writes one in its transaction, so a missing hook fails the case.
	if h.Audited == nil {
		t.Fatal("the harness has no Audited hook: the adapter must show its audit entries")
	}

	fence := h.Namespace(t, acme.String())
	h.Namespace(t, globex.String())

	before := len(h.Audited(t, acme.String()))
	beforeOther := len(h.Audited(t, globex.String()))

	setFilter(t, h, fence, full())
	setFilter(t, h, fence, emptyFilter())

	bad := emptyFilter()
	bad.ProtectedBranches = []string{"a/*"}

	if err := h.Filters.SetFilter(t.Context(), acme, bad, fence, entry(t, fence, bad)); err == nil {
		t.Fatal("SetFilter of an invalid filter succeeded")
	}

	stale := namespace.Fence{Namespace: acme, FailoverVersion: fence.FailoverVersion + 1}
	if err := h.Filters.SetFilter(t.Context(), acme, full(), stale, entry(t, stale, full())); err == nil {
		t.Fatal("SetFilter with a stale fence succeeded")
	}

	const changes = 2

	added := h.Audited(t, acme.String())[before:]
	if len(added) != changes {
		t.Fatalf("the two changes wrote %d entries, want %d", len(added), changes)
	}

	wantOldAndNew(t, added)

	if n := len(h.Audited(t, globex.String())); n != beforeOther {
		t.Fatalf("the change of the filter of acme wrote %d entries in globex, want none", n-beforeOther)
	}

	auditOldInvalid(t, h, fence)
}

// wantOldAndNew fails the test unless the entries are the change of the empty
// filter to the full filter and the change back, with the replaced value as
// the Old value of each.
func wantOldAndNew(t *testing.T, added []audit.Entry) {
	t.Helper()

	emptyText, err := source.EncodeFilter(emptyFilter())
	if err != nil {
		t.Fatalf("EncodeFilter(empty): %v", err)
	}

	fullText, err := source.EncodeFilter(full())
	if err != nil {
		t.Fatalf("EncodeFilter(full): %v", err)
	}

	for i, want := range []struct{ old, new string }{{emptyText, fullText}, {fullText, emptyText}} {
		e := added[i]
		if e.Action != filterAction || len(e.Changes) != 1 || e.Changes[0].Old == nil {
			t.Fatalf("entry %d = %+v, want one %q entry with one change that has an Old value", i, e, filterAction)
		}

		if *e.Changes[0].Old != want.old || e.Changes[0].New != want.new {
			t.Fatalf(
				"entry %d change = %q -> %q, want %q -> %q",
				i,
				*e.Changes[0].Old,
				e.Changes[0].New,
				want.old,
				want.new,
			)
		}
	}
}

// auditOldInvalid proves that the Old value over a stored value that is not a
// filter is the quoted raw value with a mark, so the entry shows what the
// write replaced.
func auditOldInvalid(t *testing.T, h Harness, fence namespace.Fence) {
	t.Helper()

	if h.FilterRaw == nil {
		return
	}

	const raw = "not a filter"

	h.FilterRaw(t, acme, raw)
	setFilter(t, h, fence, full())

	last := h.Audited(t, acme.String())
	old := last[len(last)-1].Changes[0].Old

	if old == nil || *old != invalidOld+strconv.Quote(raw) {
		t.Fatalf(
			"the Old value over a stored value that is not a filter = %v, want %q",
			old,
			invalidOld+strconv.Quote(raw),
		)
	}
}
