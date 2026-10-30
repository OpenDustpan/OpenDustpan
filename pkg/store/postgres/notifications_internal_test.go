package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

const (
	insertServerCounter = `INSERT INTO notification_sequences (scope, last_sequence) VALUES ('@server', 1)`
	insertAcmeCounter   = `INSERT INTO notification_sequences (scope, last_sequence) VALUES ('acme', 1)`
	notificationColumns = `(scope, sequence, id, type, object, object_id, subject, agent, severity, summary, links,
		attributes, recipients, dedupe_key, at_ms)`
	subscriptionColumns = `(id, scope, sink_id, types, objects, min_severity, state, reason, created_by, version)`
	deliveryColumns     = `(id, scope, notification_id, subscription_id, sink_id, contact, principal, state, attempts,
		next_at_ms, last_error, delivered_at_ms, owner, lease_until_ms, version)`
)

// TestCheckStagedNotificationsRefusesBadRows gives the restore check rows that
// the store would not have written, as if the checks of the write had failed.
// Each case names the check that must stop it.
func TestCheckStagedNotificationsRefusesBadRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		seed bool
		stmt []string
		want string
	}{
		{
			"a notification above the counter of its scope", false,
			[]string{
				insertServerCounter,
				`INSERT INTO notifications ` + notificationColumns + `
					VALUES ('@server', 5, 'ntf-1', 'job.failed', 'o', '', '', '', 2, 'x', '[]', '{}', '[]', 'k', 1)`,
			},
			"above the last sequence",
		},
		{
			"a notification with no counter for its scope", false,
			[]string{`INSERT INTO notifications ` + notificationColumns + `
					VALUES ('@server', 1, 'ntf-1', 'job.failed', 'o', '', '', '', 2, 'x', '[]', '{}', '[]', 'k', 1)`},
			"above the last sequence",
		},
		{
			"a notification with a link that is not http", false,
			[]string{
				insertServerCounter,
				`INSERT INTO notifications ` + notificationColumns + `
					VALUES ('@server', 1, 'ntf-1', 'job.failed', 'o', '', '', '', 2, 'x',
					'[{"title":"x","url":"javascript:alert(1)","kind":"console"}]', '{}', '[]', 'k', 1)`,
			},
			"staged notification",
		},
		{
			"a cursor above the counter of its scope", false,
			[]string{
				insertServerCounter,
				`INSERT INTO notification_cursors (name, scope, sequence) VALUES ('dispatch', '@server', 2)`,
			},
			"a cursor above",
		},
		{
			"a cursor with no counter for its scope", false,
			[]string{`INSERT INTO notification_cursors (name, scope, sequence) VALUES ('dispatch', '@server', 1)`},
			"a cursor above",
		},
		{
			"a cursor of a scope that names no namespace", false,
			[]string{`INSERT INTO notification_cursors (name, scope, sequence) VALUES ('dispatch', 'gone', 0)`},
			"names no namespace",
		},
		{
			"a subscription whose sink is missing", false,
			[]string{`INSERT INTO notification_subscriptions ` + subscriptionColumns + `
					VALUES ('sub-1', '@server', 'snk-none', '[]', '[]', 0, 'active', '', 'alice', 1)`},
			"sink is missing",
		},
		{
			"a subscription whose sink is in another scope", true,
			[]string{`INSERT INTO notification_subscriptions ` + subscriptionColumns + `
					VALUES ('sub-1', '@server', 'snk-aaaaaaaa', '[]', '[]', 0, 'active', '', 'alice', 1)`},
			"sink is missing or in another scope",
		},
		{
			"a delivery that holds the text of a network error", false,
			[]string{`INSERT INTO notification_deliveries ` + deliveryColumns + `
					VALUES ('dlv-1', '@server', 'ntf-1', '', '', 'a@example.com', 'alice', 'pending', 0, 1,
					'dial tcp 10.0.0.1: refused', 0, '', 0, 1)`},
			"staged delivery",
		},
		{
			"a delivery that names a sink of another scope", true,
			[]string{`INSERT INTO notification_deliveries ` + deliveryColumns + `
					VALUES ('dlv-1', '@server', 'ntf-1', '', 'snk-aaaaaaaa', 'a@example.com', 'alice', 'pending', 0, 1,
					'', 0, '', 0, 1)`},
			"names a sink of another scope",
		},
		{
			"a delivery that names a subscription of another scope", true,
			[]string{
				`INSERT INTO notification_subscriptions ` + subscriptionColumns + `
					VALUES ('sub-1', 'acme', 'snk-aaaaaaaa', '[]', '[]', 0, 'active', '', 'alice', 1)`,
				`INSERT INTO notification_deliveries ` + deliveryColumns + `
					VALUES ('dlv-1', '@server', 'ntf-1', 'sub-1', '', '', 'alice', 'pending', 0, 1, '', 0, '', 0, 1)`,
			},
			"names a subscription of another scope",
		},
		{
			"a delivery that names a notification of another scope", true,
			[]string{
				insertAcmeCounter,
				`INSERT INTO notifications ` + notificationColumns + `
					VALUES ('acme', 1, 'ntf-1', 'job.failed', 'o', '', '', '', 2, 'x', '[]', '{}', '[]', 'k', 1)`,
				`INSERT INTO notification_deliveries ` + deliveryColumns + `
					VALUES ('dlv-1', '@server', 'ntf-1', '', '', 'a@example.com', 'alice', 'pending', 0, 1, '', 0, '', 0, 1)`,
			},
			"names a notification of another scope",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openForSavedViews(t)

			if tc.seed {
				seedSink(t, s)
			}

			for _, stmt := range tc.stmt {
				if _, err := s.pool.Exec(t.Context(), stmt); err != nil {
					t.Fatalf("plant the row: %v", err)
				}
			}

			err := checkStagedNotifications(t.Context(), s.pool, currentSchema(t, s))
			if !errors.Is(err, backup.ErrCorrupt) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("check = %v, want ErrCorrupt that holds %q", err, tc.want)
			}
		})
	}
}

// TestCheckStagedNotificationsAcceptsGoodRows proves that the checks stop the
// rows above and nothing else: a database that the store wrote passes.
func TestCheckStagedNotificationsAcceptsGoodRows(t *testing.T) {
	t.Parallel()

	s := openForSavedViews(t)
	seedSink(t, s)

	for _, stmt := range []string{
		insertAcmeCounter,
		`INSERT INTO notifications ` + notificationColumns + `
			VALUES ('acme', 1, 'ntf-1', 'job.failed', 'o', '', '', '', 2, 'x', '[]', '{}', '[]', 'k', 1)`,
		`INSERT INTO notification_cursors (name, scope, sequence) VALUES ('dispatch', 'acme', 1)`,
		`INSERT INTO notification_subscriptions ` + subscriptionColumns + `
			VALUES ('sub-1', 'acme', 'snk-aaaaaaaa', '[]', '[]', 0, 'active', '', 'alice', 1)`,
		`INSERT INTO notification_deliveries ` + deliveryColumns + `
			VALUES ('dlv-1', 'acme', 'ntf-1', 'sub-1', 'snk-aaaaaaaa', '', 'alice', 'pending', 0, 1, '', 0, '', 0, 1)`,
		`INSERT INTO notification_deliveries ` + deliveryColumns + `
			VALUES ('dlv-2', 'acme', 'ntf-gone', 'sub-gone', 'snk-gone', '', 'alice', 'delivered', 1, 1, '', 1, '', 0, 2)`,
	} {
		if _, err := s.pool.Exec(t.Context(), stmt); err != nil {
			t.Fatalf("plant the row: %v", err)
		}
	}

	if err := checkStagedNotifications(t.Context(), s.pool, currentSchema(t, s)); err != nil {
		t.Fatalf("check = %v, want nil", err)
	}
}

// TestAppendNotificationsRefusesAnotherScope calls the function that a domain
// store runs in its own transaction with a notification of another scope than
// the one it appends to. It is ErrInvalid and stores nothing, so a domain
// write never puts a row into the outbox of another scope.
func TestAppendNotificationsRefusesAnotherScope(t *testing.T) {
	t.Parallel()

	s := openForSavedViews(t)
	seedSink(t, s)

	n := notify.Notification{
		ID: "ntf-1", Type: notify.JobFailed, Scope: "@server", Object: "o", Subject: "", Agent: "",
		Severity: notify.SeverityWarning, Summary: "x", DedupeKey: "k", At: time.UnixMilli(1).UTC(),
	}

	err := s.inTx(t.Context(), func(q *db.Queries) error {
		return appendNotifications(t.Context(), q, "acme", []notify.Notification{n})
	})

	if !errors.Is(err, notify.ErrInvalid) {
		t.Fatalf("append of a notification of another scope: err = %v, want ErrInvalid", err)
	}

	got, afterErr := s.Notifications().After(t.Context(), "@server", 0, 10)
	if afterErr != nil || len(got) != 0 {
		t.Fatalf("After = %v (%v), want none", got, afterErr)
	}

	got, afterErr = s.Notifications().After(t.Context(), "acme", 0, 10)
	if afterErr != nil || len(got) != 0 {
		t.Fatalf("After in acme = %v (%v), want none", got, afterErr)
	}
}
