package backup

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

// TestRestoreEventOverflow proves that the summary event of the events past
// the limit round trips its count, that the text names the count as an upper
// bound, and that an event that is not the summary is not read as one.
func TestRestoreEventOverflow(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		count int64
	}{
		{name: "one past the limit", count: 1},
		{name: "a large count", count: 1 << 40},
		{name: "the largest count", count: math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			event := restoreEventOverflow(tc.count)

			if want := "up to " + strconv.FormatInt(tc.count, 10) + " more"; !strings.Contains(event, want) {
				t.Fatalf("restoreEventOverflow(%d) = %q, want the text %q", tc.count, event, want)
			}

			got, ok := restoreEventOverflowCount(event)
			if !ok || got != tc.count {
				t.Fatalf("restoreEventOverflowCount(%q) = %d, %v, want %d, true", event, got, ok, tc.count)
			}
		})
	}

	for _, tc := range []struct {
		name  string
		event string
	}{
		{name: "another audit event", event: "restore cancel rst-abcdefghijklmnopqr27"},
		{name: "an empty event", event: ""},
		{name: "the head alone", event: restoreEventOverflowHead},
		{
			name:  "the form without the bound",
			event: "restore audit events past the limit of 999: 5 more cancels or refusals, in the log lines",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got, ok := restoreEventOverflowCount(tc.event); ok {
				t.Fatalf("restoreEventOverflowCount(%q) = %d, true, want ok false", tc.event, got)
			}
		})
	}
}
