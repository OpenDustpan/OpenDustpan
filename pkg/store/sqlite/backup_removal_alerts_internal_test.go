package sqlite

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestCheckStagedRemovalAlerts proves the restore check of the alert markers:
// a marker after the time of the restore is corrupt, a marker at that time
// passes, and a zero restore time refuses every marker row.
func TestCheckStagedRemovalAlerts(t *testing.T) {
	t.Parallel()

	restoreAt := time.UnixMilli(1_700_000_000_000).UTC()

	cases := []struct {
		name      string
		restoreAt time.Time
		marker    *time.Time
		want      error
	}{
		{
			name: "a marker one millisecond after the restore", restoreAt: restoreAt,
			marker: new(restoreAt.Add(time.Millisecond)), want: backup.ErrCorrupt,
		},
		{name: "a marker at the restore time", restoreAt: restoreAt, marker: new(restoreAt), want: nil},
		{
			name: "a marker before the restore", restoreAt: restoreAt,
			marker: new(restoreAt.Add(-time.Hour)), want: nil,
		},
		{
			name: "a zero restore time with a marker", restoreAt: time.Time{},
			marker: new(restoreAt), want: backup.ErrCorrupt,
		},
		{name: "a zero restore time with no marker", restoreAt: time.Time{}, marker: nil, want: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)

			insertRemovalAlertMarker(t, s, tc.marker)
			checkStagedRemovalAlertsResult(t, s, tc.restoreAt, tc.want)
		})
	}
}

// insertRemovalAlertMarker inserts one marker row at marker, and no row for a
// nil marker.
func insertRemovalAlertMarker(t *testing.T, s *Store, marker *time.Time) {
	t.Helper()

	if marker == nil {
		return
	}

	_, err := s.conn.ExecContext(
		t.Context(),
		`INSERT INTO member_removal_alerts (source_kind, source_id, alerted_at_ms)
VALUES ('identity-provider', 'idp-1', ?)`,
		marker.UnixMilli(),
	)
	if err != nil {
		t.Fatalf("insert the marker: %v", err)
	}
}

// checkStagedRemovalAlertsResult runs the check and compares its error with
// want. A refusal must name the source kind and the source ID.
func checkStagedRemovalAlertsResult(t *testing.T, s *Store, restoreAt time.Time, want error) {
	t.Helper()

	err := checkStagedRemovalAlerts(t.Context(), s.conn, restoreAt)

	switch {
	case want == nil && err != nil:
		t.Fatalf("check = %v, want nil", err)
	case want == nil:
	case err == nil:
		t.Fatalf("check = nil, want %v", want)
	case !errors.Is(err, want):
		t.Fatalf("check = %v, want %v", err, want)
	default:
		for _, text := range []string{"identity-provider", "idp-1"} {
			if !strings.Contains(err.Error(), text) {
				t.Fatalf("check = %v, want it to name %q", err, text)
			}
		}
	}
}
