package principaltest

import (
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/principal"
)

// RemovalLogStore is the store that RunRemovalLog checks: the removal log
// and a read of its rows, which the port does not hold. The checks use Rows
// to prove what the log stores and what it deletes.
type RemovalLogStore struct {
	// Log is the log under test.
	Log principal.RemovalLog
	// Rows returns every row of the log, oldest first.
	Rows func(t *testing.T) []principal.Removal
}

// RunRemovalLog runs the conformance suite of a principal.RemovalLog
// against logs produced by newStore.
func RunRemovalLog(t *testing.T, newStore func(t *testing.T) RemovalLogStore) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, s RemovalLogStore)
	}{
		{"ARemovalOfManyMembersGivesItsOwnTotals", testRemovalTotals},
		{"RemovalsOfOneMemberSumUp", testRemovalSum},
		{"ARowOlderThanTheWindowIsNotCounted", testRemovalOlderThanWindow},
		{"ARowAtTheWindowEdgeIsNotCounted", testRemovalAtWindowEdge},
		{"ARowJustInsideTheWindowIsCounted", testRemovalInsideWindow},
		{"AnotherSourceAndAnotherGroupAreKeptApart", testRemovalApart},
		{"TheLargestGroupOfTheWindowGivesTheMembers", testRemovalLargestGroup},
		{"ARemovalThatFailsTheChecksIsErrInvalid", testRemovalInvalid},
		{"AWindowThatFailsTheChecksIsErrInvalid", testRemovalInvalidWindow},
		{"RowsOlderThanTheMaximumWindowAreDeleted", testRemovalPrune},
		{"ThePruneDeletesAtMostItsLimit", testRemovalPruneLimit},
		{"ARemovalWithNoAlertHasNoAlertedAt", testRemovalNoAlert},
		{"AnAlertMarkerShowsInTheTotals", testRemovalAlertedAt},
		{"ASecondAlertMarkerReplacesTheTime", testRemovalAlertReplaces},
		{"TheMarkerOfOneSourceStaysOutOfAnother", testRemovalAlertApart},
		{"AnAlertMarkerThatFailsTheChecksIsErrInvalid", testRemovalAlertInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// removalWindow is the window of the removal checks: one hour.
const removalWindow = time.Hour

// removalSource returns the source of the removals of the checks.
func removalSource() principal.RemovalSource {
	return principal.RemovalSource{Kind: principal.RemovalSourceIdentityProvider, ID: "idp-1"}
}

// removalBase returns the fixed base time of the checks. Every time of the
// suite is an offset from it, so no check reads the wall clock.
func removalBase() time.Time {
	return at(0)
}

// removalOf returns a removal of removed members of the group by source, at
// the offset from the base time.
func removalOf(
	source principal.RemovalSource, groupID string, removed, membersBefore int, offset time.Duration,
) principal.Removal {
	return principal.Removal{
		Source:        source,
		GroupID:       groupID,
		Removed:       removed,
		MembersBefore: membersBefore,
		At:            removalBase().Add(offset),
	}
}

// newRemoval returns a removal by the source of the checks.
func newRemoval(groupID string, removed, membersBefore int, offset time.Duration) principal.Removal {
	return removalOf(removalSource(), groupID, removed, membersBefore, offset)
}

// addRemoval stores one removal with the window of the checks and fails the
// test on an error.
func addRemoval(t *testing.T, s RemovalLogStore, r principal.Removal) principal.RemovalTotals {
	t.Helper()

	totals, err := s.Log.AddRemoval(t.Context(), r, removalWindow)
	if err != nil {
		t.Fatalf("add a removal of %d members at %s: %v", r.Removed, r.At, err)
	}

	return totals
}

// newTotals returns the totals of a window in which the source removed
// source members, group of them from the group, when the group held members
// before the largest of those removals.
func newTotals(source, group, members int) principal.RemovalTotals {
	return principal.RemovalTotals{
		SourceRemoved: source, GroupRemoved: group, GroupMembers: members, AlertedAt: time.Time{},
	}
}

// wantTotals fails the test unless the totals are the wanted ones.
func wantTotals(t *testing.T, what string, got, want principal.RemovalTotals) {
	t.Helper()

	if got.SourceRemoved != want.SourceRemoved || got.GroupRemoved != want.GroupRemoved ||
		got.GroupMembers != want.GroupMembers || !got.AlertedAt.Equal(want.AlertedAt) {
		t.Fatalf("%s: totals = %+v, want %+v", what, got, want)
	}
}

// markAlert stores the alert time at of source and fails the test on an
// error.
func markAlert(t *testing.T, s RemovalLogStore, source principal.RemovalSource, at time.Time) {
	t.Helper()

	if err := s.Log.MarkRemovalAlert(t.Context(), source, at); err != nil {
		t.Fatalf("mark an alert of the source at %s: %v", at, err)
	}
}

// wantRows fails the test unless the log holds the rows, oldest first.
func wantRows(t *testing.T, s RemovalLogStore, want ...principal.Removal) {
	t.Helper()

	got := s.Rows(t)
	if len(got) != len(want) {
		t.Fatalf("the log holds %d rows, want %d:\ngot  %+v\nwant %+v", len(got), len(want), got, want)
	}

	for i, row := range got {
		if row.Source != want[i].Source || row.GroupID != want[i].GroupID || row.Removed != want[i].Removed ||
			row.MembersBefore != want[i].MembersBefore || !row.At.Equal(want[i].At) {
			t.Fatalf("row %d = %+v, want %+v", i, row, want[i])
		}
	}
}

// testRemovalTotals checks that one removal of many members gives the totals
// of that row.
func testRemovalTotals(t *testing.T, s RemovalLogStore) {
	t.Helper()

	totals := addRemoval(t, s, newRemoval("grp-eng", 40, 100, 0))
	wantTotals(t, "one removal of 40 members", totals, newTotals(40, 40, 100))
}

// testRemovalSum checks that removals of one member each sum up, and that
// the largest group of the window gives the members before the removals.
func testRemovalSum(t *testing.T, s RemovalLogStore) {
	t.Helper()

	addRemoval(t, s, newRemoval("grp-eng", 1, 50, 0))
	addRemoval(t, s, newRemoval("grp-eng", 1, 49, time.Minute))

	totals := addRemoval(t, s, newRemoval("grp-eng", 1, 48, 2*time.Minute))
	wantTotals(t, "three removals of one member", totals, newTotals(3, 3, 50))
}

// testRemovalOlderThanWindow checks that a row outside the window of the new
// removal is not counted.
func testRemovalOlderThanWindow(t *testing.T, s RemovalLogStore) {
	t.Helper()

	addRemoval(t, s, newRemoval("grp-eng", 5, 50, 0))

	totals := addRemoval(t, s, newRemoval("grp-eng", 1, 45, 2*removalWindow))
	wantTotals(t, "a row of two hours before a window of one hour", totals, newTotals(1, 1, 45))
}

// testRemovalAtWindowEdge checks that a row exactly at the start of the
// window is not counted.
func testRemovalAtWindowEdge(t *testing.T, s RemovalLogStore) {
	t.Helper()

	addRemoval(t, s, newRemoval("grp-eng", 5, 50, 0))

	totals := addRemoval(t, s, newRemoval("grp-eng", 1, 45, removalWindow))
	wantTotals(t, "a row at the start of the window", totals, newTotals(1, 1, 45))
}

// testRemovalInsideWindow checks that a row one millisecond inside the
// window is counted.
func testRemovalInsideWindow(t *testing.T, s RemovalLogStore) {
	t.Helper()

	addRemoval(t, s, newRemoval("grp-eng", 5, 50, 0))
	addRemoval(t, s, newRemoval("grp-eng", 4, 45, time.Millisecond))

	totals := addRemoval(t, s, newRemoval("grp-eng", 1, 41, removalWindow))
	wantTotals(t, "a row one millisecond inside the window", totals, newTotals(5, 5, 45))
}

// testRemovalApart checks that the totals keep another source and another
// group apart. The other source differs once in its ID and once in its kind.
func testRemovalApart(t *testing.T, s RemovalLogStore) {
	t.Helper()

	otherID := principal.RemovalSource{Kind: principal.RemovalSourceIdentityProvider, ID: "idp-2"}
	otherKind := principal.RemovalSource{Kind: principal.RemovalSourceSCIMToken, ID: removalSource().ID}

	addRemoval(t, s, newRemoval("grp-eng", 2, 10, 0))
	addRemoval(t, s, newRemoval("grp-ops", 5, 20, time.Minute))
	addRemoval(t, s, removalOf(otherID, "grp-eng", 7, 30, 2*time.Minute))
	addRemoval(t, s, removalOf(otherKind, "grp-eng", 11, 40, 3*time.Minute))

	totals := addRemoval(t, s, newRemoval("grp-eng", 1, 9, 4*time.Minute))
	wantTotals(t, "another source and another group", totals, newTotals(8, 3, 10))
}

// testRemovalLargestGroup checks that the largest number of members before a
// removal of the group gives the members, whatever the order of the rows.
func testRemovalLargestGroup(t *testing.T, s RemovalLogStore) {
	t.Helper()

	addRemoval(t, s, newRemoval("grp-eng", 1, 5, 0))
	addRemoval(t, s, newRemoval("grp-eng", 1, 50, time.Minute))
	addRemoval(t, s, newRemoval("grp-eng", 1, 20, 2*time.Minute))

	totals := addRemoval(t, s, newRemoval("grp-eng", 1, 4, 3*time.Minute))
	wantTotals(t, "the largest group of the window", totals, newTotals(4, 4, 50))
}

// removalEdit is one row of the invalid cases: a name and the change that
// breaks one rule of Removal.Validate.
type removalEdit struct {
	name string
	edit func(r *principal.Removal)
}

// invalidRemovals returns one removal that breaks each rule of
// Removal.Validate.
func invalidRemovals() []removalEdit {
	return []removalEdit{
		{name: "an unknown source kind", edit: func(r *principal.Removal) { r.Source.Kind = "saml" }},
		{name: "an empty source ID", edit: func(r *principal.Removal) { r.Source.ID = "" }},
		{name: "a source ID of 257 bytes", edit: func(r *principal.Removal) {
			r.Source.ID = strings.Repeat("a", 257)
		}},
		{name: "a source ID with a control character", edit: func(r *principal.Removal) {
			r.Source.ID = "idp-\n1"
		}},
		{name: "an empty group ID", edit: func(r *principal.Removal) { r.GroupID = "" }},
		{name: "a group ID of 257 bytes", edit: func(r *principal.Removal) {
			r.GroupID = strings.Repeat("a", 257)
		}},
		{name: "a group ID with a control character", edit: func(r *principal.Removal) {
			r.GroupID = "grp-\x00eng"
		}},
		{name: "a removal of no members", edit: func(r *principal.Removal) { r.Removed = 0 }},
		{name: "a removal of more members than the group held", edit: func(r *principal.Removal) {
			r.Removed, r.MembersBefore = 11, 10
		}},
		{name: "a removal with no time", edit: func(r *principal.Removal) { r.At = time.Time{} }},
	}
}

// testRemovalInvalid checks that each removal that fails the checks gives
// ErrInvalid and stores nothing.
func testRemovalInvalid(t *testing.T, s RemovalLogStore) {
	t.Helper()

	for _, tc := range invalidRemovals() {
		t.Run(tc.name, func(t *testing.T) {
			r := newRemoval("grp-eng", 1, 10, 0)
			tc.edit(&r)

			_, err := s.Log.AddRemoval(t.Context(), r, removalWindow)
			wantErr(t, "add an invalid removal", err, principal.ErrInvalid)

			if rows := s.Rows(t); len(rows) != 0 {
				t.Errorf("the log stored %d rows of an invalid removal: %+v", len(rows), rows)
			}
		})
	}
}

// testRemovalInvalidWindow checks that a window outside its bounds gives
// ErrInvalid and stores nothing.
func testRemovalInvalidWindow(t *testing.T, s RemovalLogStore) {
	t.Helper()

	windows := []struct {
		name   string
		window time.Duration
	}{
		{name: "a window of zero", window: 0},
		{name: "a negative window", window: -time.Second},
		{name: "a window above the maximum", window: principal.MaxRemovalWindow + time.Millisecond},
	}

	for _, tc := range windows {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Log.AddRemoval(t.Context(), newRemoval("grp-eng", 1, 10, 0), tc.window)
			wantErr(t, "add a removal with "+tc.name, err, principal.ErrInvalid)

			if rows := s.Rows(t); len(rows) != 0 {
				t.Errorf("the log stored %d rows of an invalid window: %+v", len(rows), rows)
			}
		})
	}
}

// testRemovalPrune checks that a write deletes the rows of its own source
// that are older than the maximum window, and keeps the rows of another
// source and the rows of the window.
func testRemovalPrune(t *testing.T, s RemovalLogStore) {
	t.Helper()

	other := principal.RemovalSource{Kind: principal.RemovalSourceIdentityProvider, ID: "idp-2"}

	addRemoval(t, s, newRemoval("grp-eng", 1, 10, 0))
	addRemoval(t, s, newRemoval("grp-eng", 1, 9, time.Hour))
	addRemoval(t, s, newRemoval("grp-eng", 1, 8, 2*time.Hour))
	addRemoval(t, s, removalOf(other, "grp-eng", 1, 10, 0))

	totals := addRemoval(t, s, newRemoval("grp-eng", 1, 7, principal.MaxRemovalWindow+time.Hour))
	wantTotals(t, "a removal above the maximum window", totals, newTotals(1, 1, 7))

	wantRows(t, s,
		removalOf(other, "grp-eng", 1, 10, 0),
		newRemoval("grp-eng", 1, 8, 2*time.Hour),
		newRemoval("grp-eng", 1, 7, principal.MaxRemovalWindow+time.Hour),
	)
}

// testRemovalPruneLimit checks that one write deletes at most
// MaxRemovalRowsPruned rows.
func testRemovalPruneLimit(t *testing.T, s RemovalLogStore) {
	t.Helper()

	rows := principal.MaxRemovalRowsPruned + 2

	for range rows {
		addRemoval(t, s, newRemoval("grp-eng", 1, rows, 0))
	}

	totals := addRemoval(t, s, newRemoval("grp-eng", 1, 1, principal.MaxRemovalWindow+time.Hour))
	wantTotals(t, "a removal above the maximum window", totals, newTotals(1, 1, 1))

	wantRows(t, s,
		newRemoval("grp-eng", 1, rows, 0),
		newRemoval("grp-eng", 1, rows, 0),
		newRemoval("grp-eng", 1, 1, principal.MaxRemovalWindow+time.Hour),
	)
}

// testRemovalNoAlert checks that a source without an alert has no alerted
// time.
func testRemovalNoAlert(t *testing.T, s RemovalLogStore) {
	t.Helper()

	totals := addRemoval(t, s, newRemoval("grp-eng", 1, 10, 0))
	wantTotals(t, "a source without an alert", totals, newTotals(1, 1, 10))
}

// testRemovalAlertedAt checks that the totals hold the time of the last
// alert of the source.
func testRemovalAlertedAt(t *testing.T, s RemovalLogStore) {
	t.Helper()

	alertedAt := removalBase().Add(3*time.Minute + 123*time.Millisecond + 456*time.Microsecond)
	markAlert(t, s, removalSource(), alertedAt)

	totals := addRemoval(t, s, newRemoval("grp-eng", 1, 10, 5*time.Minute))

	want := newTotals(1, 1, 10)
	want.AlertedAt = time.UnixMilli(alertedAt.UnixMilli()).UTC()
	wantTotals(t, "a source with an alert", totals, want)
}

// testRemovalAlertReplaces checks that a second marker replaces the time of
// the first.
func testRemovalAlertReplaces(t *testing.T, s RemovalLogStore) {
	t.Helper()

	markAlert(t, s, removalSource(), removalBase().Add(time.Minute))
	alertedAt := removalBase().Add(2 * time.Minute)
	markAlert(t, s, removalSource(), alertedAt)

	totals := addRemoval(t, s, newRemoval("grp-eng", 1, 10, 4*time.Minute))

	want := newTotals(1, 1, 10)
	want.AlertedAt = alertedAt
	wantTotals(t, "a second marker", totals, want)
}

// testRemovalAlertApart checks that the marker of another source does not
// show in the totals: another kind with the same ID, and the same kind with
// another ID.
func testRemovalAlertApart(t *testing.T, s RemovalLogStore) {
	t.Helper()

	otherKind := principal.RemovalSource{Kind: principal.RemovalSourceSCIMToken, ID: removalSource().ID}
	otherID := principal.RemovalSource{Kind: principal.RemovalSourceIdentityProvider, ID: "idp-2"}
	otherKindAt := removalBase().Add(time.Minute)
	otherIDAt := removalBase().Add(2 * time.Minute)

	markAlert(t, s, otherKind, otherKindAt)
	markAlert(t, s, otherID, otherIDAt)

	totals := addRemoval(t, s, newRemoval("grp-eng", 1, 10, 3*time.Minute))
	wantTotals(t, "another kind and another ID", totals, newTotals(1, 1, 10))

	totals = addRemoval(t, s, removalOf(otherKind, "grp-eng", 1, 10, 4*time.Minute))
	want := newTotals(1, 1, 10)
	want.AlertedAt = otherKindAt
	wantTotals(t, "the other kind", totals, want)

	totals = addRemoval(t, s, removalOf(otherID, "grp-eng", 1, 10, 5*time.Minute))
	want = newTotals(1, 1, 10)
	want.AlertedAt = otherIDAt
	wantTotals(t, "the other ID", totals, want)
}

// testRemovalAlertInvalid checks that each marker that fails the checks gives
// ErrInvalid and stores nothing.
func testRemovalAlertInvalid(t *testing.T, s RemovalLogStore) {
	t.Helper()

	cases := []struct {
		name   string
		source principal.RemovalSource
		at     time.Time
	}{
		{
			name:   "an unknown source kind",
			source: principal.RemovalSource{Kind: "saml", ID: removalSource().ID},
			at:     removalBase(),
		},
		{
			name:   "an empty source ID",
			source: principal.RemovalSource{Kind: principal.RemovalSourceIdentityProvider, ID: ""},
			at:     removalBase(),
		},
		{
			name:   "no time",
			source: removalSource(),
			at:     time.Time{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.Log.MarkRemovalAlert(t.Context(), tc.source, tc.at)
			wantErr(t, "mark an invalid alert", err, principal.ErrInvalid)

			totals := addRemoval(t, s, newRemoval("grp-eng", 1, 10, 0))
			if !totals.AlertedAt.IsZero() {
				t.Fatalf("an invalid marker stored the alert time %s", totals.AlertedAt)
			}
		})
	}
}
