package backup_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestAddRestoreEventPastTheLimit proves that the events of a restore never
// refuse a cancel or a repair for the limit: past the limit, the last place
// holds one summary event with an upper bound of the events that did not fit.
func TestAddRestoreEventPastTheLimit(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		extra     int
		wantCount int
	}{
		{name: "full with no summary", extra: -1, wantCount: 0},
		{name: "one past the limit", extra: 0, wantCount: 1},
		{name: "many past the limit", extra: 250, wantCount: 251},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stored := addRestoreEvents(t, backup.MaxRestoreEvents+tc.extra)
			checkRestoreEventSummary(t, stored, tc.wantCount)
		})
	}
}

// addRestoreEvents adds n distinct events to an empty list and returns the
// stored text.
func addRestoreEvents(t *testing.T, n int) string {
	t.Helper()

	stored := "[]"

	for i := range n {
		next, changed, err := backup.AddRestoreEvent(stored, "restore cancel "+strconv.Itoa(i))
		if err != nil || !changed {
			t.Fatalf("add event %d: changed %v, %v", i, changed, err)
		}

		stored = next
	}

	return stored
}

// checkRestoreEventSummary proves the events of stored: below the limit no
// summary, past it the limit with a summary of wantCount. An event that the
// list already holds changes nothing.
func checkRestoreEventSummary(t *testing.T, stored string, wantCount int) {
	t.Helper()

	events, err := backup.DecodeRestoreEvents(stored)
	if err != nil {
		t.Fatalf("decode the events: %v", err)
	}

	want := backup.MaxRestoreEvents
	if wantCount == 0 {
		want = backup.MaxRestoreEvents - 1
	}

	if len(events) != want {
		t.Fatalf("events = %d, want %d", len(events), want)
	}

	if summary := events[len(events)-1]; wantCount > 0 &&
		!strings.Contains(summary, "up to "+strconv.Itoa(wantCount)+" more") {
		t.Fatalf("summary event = %q, want the count %d", summary, wantCount)
	}

	again, changed, err := backup.AddRestoreEvent(stored, "restore cancel 0")
	if err != nil || changed || again != stored {
		t.Fatalf("add a held event again: changed %v, %v; want no change", changed, err)
	}
}

// TestAddRestoreEventRefusesTheSummaryForm proves that an object that starts
// with the text of the summary event cannot reach the events, so no caller
// can forge the count.
func TestAddRestoreEventRefusesTheSummaryForm(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		object string
	}{
		{
			name:   "the old form",
			object: "restore audit events past the limit of 999: 5 more cancels",
		},
		{
			name:   "the new form",
			object: "restore audit events past the limit of 999: up to 5 more cancels",
		},
		{
			name:   "the prefix alone",
			object: "restore audit events past the limit of 999: ",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := backup.AddRestoreEvent("[]", tc.object)
			if !errors.Is(err, backup.ErrRestoreEventInvalid) {
				t.Fatalf("add %q: err = %v, want ErrRestoreEventInvalid", tc.object, err)
			}
		})
	}
}
