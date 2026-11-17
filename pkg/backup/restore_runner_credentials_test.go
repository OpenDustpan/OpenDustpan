package backup_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
)

// droppedAt is the time of the audit entries of the drop cases.
func droppedAt() time.Time {
	return time.Date(2026, time.May, 4, 9, 30, 0, 0, time.UTC)
}

// TestDroppedRunnerCredentialsObject checks the object text of the audit
// entry of the runner credentials that a restore drops: the counts, the
// runner IDs sorted and with no repetition, and no credential.
func TestDroppedRunnerCredentialsObject(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   backup.DroppedRunnerCredentials
		want string
	}{
		{
			name: "one handoff token and one job session of one runner",
			in:   backup.NewDroppedRunnerCredentials(1, 1, []string{"rn-b"}),
			want: "restore dropped 1 handoff token and 1 job session: the restored data does not hold runner rn-b",
		},
		{
			name: "several credentials of several runners in another order and with a repeat",
			in:   backup.NewDroppedRunnerCredentials(2, 3, []string{"rn-c", "rn-a", "rn-c"}),
			want: "restore dropped 2 handoff tokens and 3 job sessions: the restored data does not hold " +
				"runners rn-a, rn-c",
		},
		{
			name: "no handoff token",
			in:   backup.NewDroppedRunnerCredentials(0, 1, []string{"rn-a"}),
			want: "restore dropped 0 handoff tokens and 1 job session: the restored data does not hold runner rn-a",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.in.Object(); got != tc.want {
				t.Errorf("object = %q, want %q", got, tc.want)
			}

			entry := tc.in.Entry(droppedAt())

			if entry.Action != "restore:drop-runner-credentials" || entry.Subject != "@server" ||
				entry.Namespace != audit.ServerScope || !entry.At.Equal(droppedAt()) {
				t.Errorf("entry = %+v, want the drop of the runner credentials at %v", entry, droppedAt())
			}
		})
	}
}

// TestDroppedRunnerCredentialsEmpty checks the record that writes no entry.
func TestDroppedRunnerCredentialsEmpty(t *testing.T) {
	t.Parallel()

	if got := backup.NewDroppedRunnerCredentials(0, 0, nil); !got.Empty() {
		t.Errorf("record of no dropped credential = %+v, want Empty", got)
	}

	if got := backup.NewDroppedRunnerCredentials(1, 0, []string{"rn-a"}); got.Empty() {
		t.Errorf("record of a dropped handoff token = %+v, want not Empty", got)
	}

	if got := backup.NewDroppedRunnerCredentials(0, 1, []string{"rn-a"}); got.Empty() {
		t.Errorf("record of a dropped job session = %+v, want not Empty", got)
	}
}
