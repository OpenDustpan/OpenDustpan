package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/notifystore"
)

// stagedScopeQuery lists the scopes of the notification tables of a staged
// backup that name no namespace of the backup and are not the server scope.
const stagedScopeQuery = `SELECT DISTINCT t.scope FROM (
    SELECT scope FROM notification_sinks
    UNION SELECT scope FROM notification_sink_secrets
    UNION SELECT scope FROM notification_sink_refs
    UNION SELECT scope FROM notifications
    UNION SELECT scope FROM notification_sequences
    UNION SELECT scope FROM notification_cursors
    UNION SELECT scope FROM notification_subscriptions
    UNION SELECT scope FROM notification_deliveries
) AS t
WHERE t.scope <> '@server' AND NOT EXISTS (SELECT 1 FROM namespaces AS n WHERE n.name = t.scope)
LIMIT 1`

// stagedSequenceQuery finds a notification of a staged backup above the last
// sequence of its scope. A restore would then make a sequence twice.
const stagedSequenceQuery = `SELECT n.scope FROM notifications AS n
LEFT JOIN notification_sequences AS s ON s.scope = n.scope
WHERE s.last_sequence IS NULL OR n.sequence > s.last_sequence
LIMIT 1`

// stagedCursorQuery finds a cursor of a staged backup above the last sequence
// of its scope. A dispatcher would then skip the notifications up to it.
const stagedCursorQuery = `SELECT c.name FROM notification_cursors AS c
LEFT JOIN notification_sequences AS s ON s.scope = c.scope
WHERE c.sequence > COALESCE(s.last_sequence, 0)
LIMIT 1`

// The queries below find a reference from one row to a row of another scope in
// a staged backup. A subscription needs a sink of its own scope. A delivery may
// name a sink, a subscription or a notification that the store no longer holds,
// but never one that another scope owns.
const (
	stagedItemSinkQuery = `SELECT s.ref FROM notification_sink_secrets AS s
LEFT JOIN notification_sinks AS k ON k.id = s.sink_id
WHERE k.id IS NULL OR k.scope <> s.scope
LIMIT 1`
	stagedSubscriptionSinkQuery = `SELECT s.id FROM notification_subscriptions AS s
LEFT JOIN notification_sinks AS k ON k.id = s.sink_id
WHERE k.id IS NULL OR k.scope <> s.scope
LIMIT 1`
	stagedDeliverySinkQuery = `SELECT d.id FROM notification_deliveries AS d
JOIN notification_sinks AS k ON k.id = d.sink_id
WHERE k.scope <> d.scope
LIMIT 1`
	stagedDeliverySubscriptionQuery = `SELECT d.id FROM notification_deliveries AS d
JOIN notification_subscriptions AS s ON s.id = d.subscription_id
WHERE s.scope <> d.scope
LIMIT 1`
	stagedDeliveryNotificationQuery = `SELECT d.id FROM notification_deliveries AS d
JOIN notifications AS n ON n.id = d.notification_id
WHERE n.scope <> d.scope
LIMIT 1`
)

// checkStagedNotifications checks the notification tables of a staged backup
// with the rules of a stored row: every scope is a namespace of the backup or
// the server scope, the counter of each scope covers its notifications, and
// each notification, subscription and delivery passes the checks of its
// model. A row that fails makes the backup corrupt, so a restore never brings
// in a row that the server would not serve. It returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedNotifications(ctx context.Context, conn *sql.DB) error {
	for _, check := range []struct {
		query string
		what  string
	}{
		{stagedScopeQuery, "a scope that names no namespace of the backup"},
		{stagedSequenceQuery, "a notification above the last sequence of its scope"},
		{stagedCursorQuery, "a cursor above the last sequence of its scope"},
		{stagedItemSinkQuery, "a sink secret whose sink is missing or in another scope"},
		{stagedSubscriptionSinkQuery, "a subscription whose sink is missing or in another scope"},
		{stagedDeliverySinkQuery, "a delivery that names a sink of another scope"},
		{stagedDeliverySubscriptionQuery, "a delivery that names a subscription of another scope"},
		{stagedDeliveryNotificationQuery, "a delivery that names a notification of another scope"},
	} {
		var scope string

		err := conn.QueryRowContext(ctx, check.query).Scan(&scope)

		switch {
		case err == nil:
			return fmt.Errorf("staged notifications: %s (%q): %w", check.what, scope, backup.ErrCorrupt)
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("read staged notifications: %w", backup.ErrExportRead)
		default:
			// No row breaks this rule.
		}
	}

	if err := checkStagedNotificationRows(ctx, conn); err != nil {
		return err
	}

	if err := checkStagedSubscriptionRows(ctx, conn); err != nil {
		return err
	}

	return checkStagedDeliveryRows(ctx, conn)
}

func checkStagedNotificationRows(ctx context.Context, conn *sql.DB) error {
	return runStaged[notifystore.NotificationRow](
		ctx,
		conn,
		"notification",
		"SELECT scope, sequence, id, type, object, object_id, subject, agent, severity, summary, links, attributes, recipients, dedupe_key, at_ms FROM notifications",
	)
}

func checkStagedSubscriptionRows(ctx context.Context, conn *sql.DB) error {
	return runStaged[notifystore.SubscriptionRow](
		ctx,
		conn,
		"subscription",
		"SELECT id, scope, sink_id, types, objects, min_severity, state, reason, created_by, version FROM notification_subscriptions",
	)
}

func checkStagedDeliveryRows(ctx context.Context, conn *sql.DB) error {
	return runStaged[notifystore.DeliveryRow](
		ctx,
		conn,
		"delivery",
		"SELECT id, scope, notification_id, subscription_id, sink_id, contact, principal, state, attempts, next_at_ms, last_error, delivered_at_ms, owner, lease_until_ms, version FROM notification_deliveries",
	)
}

// runStaged checks every row that the query reads, as a row of the type R.
func runStaged[R any, P interface {
	*R
	notifystore.StagedRow
}](ctx context.Context, conn *sql.DB, what, query string) error {
	return eachStaged(ctx, conn, stagedQuery{what: what, sql: query}, func(rows *sql.Rows) error {
		var r R

		row := P(&r)

		if err := rows.Scan(row.Dest()...); err != nil {
			return fmt.Errorf("read staged %s: %w", what, backup.ErrExportRead)
		}

		if id, err := row.Check(); err != nil {
			return fmt.Errorf("staged %s %q: %w: %w", what, id, err, backup.ErrCorrupt)
		}

		return nil
	})
}

// stagedQuery names a query of a staged table and what it reads, for the text
// of an error.
type stagedQuery struct {
	what string
	sql  string
}

// eachStaged runs q and calls check for each row. It stops at the first error
// of check, and closes the rows.
func eachStaged(ctx context.Context, conn *sql.DB, q stagedQuery, check func(rows *sql.Rows) error) error {
	rows, err := conn.QueryContext(ctx, q.sql)
	if err != nil {
		return fmt.Errorf("read staged %s: %w", q.what, backup.ErrExportRead)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	for rows.Next() {
		if checkErr := check(rows); checkErr != nil {
			return checkErr
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged %s: %w", q.what, backup.ErrExportRead)
	}

	return nil
}
