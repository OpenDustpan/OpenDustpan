package postgres

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

// TestCheckStagedNotificationsRefusesBadRows gives the restore check rows that
// the store would not have written, as if the checks of the write had failed.
func TestCheckStagedNotificationsRefusesBadRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		stmt string
	}{
		{
			"a notification above the counter of its scope",
			`INSERT INTO notifications (scope, sequence, id, type, object, object_id, subject, agent, severity,
				summary, links, attributes, recipients, dedupe_key, at_ms)
				VALUES ('@server', 5, 'ntf-1', 'job.failed', 'o', '', '', '', 2, 'x', '[]', '{}', '[]', 'k', 1)`,
		},
		{
			"a notification with a link that is not http",
			`INSERT INTO notification_sequences (scope, last_sequence) VALUES ('@server', 1);
			INSERT INTO notifications (scope, sequence, id, type, object, object_id, subject, agent, severity,
				summary, links, attributes, recipients, dedupe_key, at_ms)
				VALUES ('@server', 1, 'ntf-1', 'job.failed', 'o', '', '', '', 2, 'x',
				'[{"title":"x","url":"javascript:alert(1)","kind":"console"}]', '{}', '[]', 'k', 1)`,
		},
		{
			"a subscription with an unknown state",
			`INSERT INTO notification_subscriptions (id, scope, sink_id, types, objects, min_severity, state, reason,
				created_by, version) VALUES ('sub-1', '@server', 'snk-1', '[]', '[]', 0, 'weird', '', 'alice', 1)`,
		},
		{
			"a delivery that holds the text of a network error",
			`INSERT INTO notification_deliveries (id, scope, notification_id, subscription_id, sink_id, contact,
				principal, state, attempts, next_at_ms, last_error, delivered_at_ms, owner, lease_until_ms, version)
				VALUES ('dlv-1', '@server', 'ntf-1', 'sub-1', 'snk-1', '', 'alice', 'pending', 0, 1,
				'dial tcp 10.0.0.1: refused', 0, '', 0, 1)`,
		},
		{
			"a cursor of a scope that names no namespace",
			`INSERT INTO notification_cursors (name, scope, sequence) VALUES ('dispatch', 'gone', 1)`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openForSavedViews(t)

			for _, stmt := range splitStatements(tc.stmt) {
				if _, err := s.pool.Exec(t.Context(), stmt); err != nil {
					t.Fatalf("plant the row: %v", err)
				}
			}

			if err := checkStagedNotifications(t.Context(), s.pool, currentSchema(t, s)); !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("check = %v, want ErrCorrupt", err)
			}
		})
	}
}

func splitStatements(s string) []string {
	var out []string

	start := 0
	for i := range len(s) {
		if s[i] == ';' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}

	return append(out, s[start:])
}
