package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/notifystore"
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
func checkStagedNotifications(ctx context.Context, pool pgQuerier, staging string) error {
	for _, check := range stagedNotificationChecks(staging) {
		var scope string

		err := pool.QueryRow(ctx, check.query).Scan(&scope)

		switch {
		case err == nil:
			return fmt.Errorf("staged notifications: %s (%q): %w", check.what, scope, backup.ErrCorrupt)
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("read staged notifications: %w", backup.ErrExportRead)
		default:
			// No row breaks this rule.
		}
	}

	if err := checkStagedNotificationRows(ctx, pool, staging); err != nil {
		return err
	}

	if err := checkStagedSubscriptionRows(ctx, pool, staging); err != nil {
		return err
	}

	return checkStagedDeliveryRows(ctx, pool, staging)
}

// stagedCheck is a query on a staged backup that finds the first row which
// breaks a rule of the notification tables, and the name of that rule.
type stagedCheck struct {
	query string
	what  string
}

// stagedNotificationChecks returns the checks on the notification tables of
// the staged backup in the schema staging.
func stagedNotificationChecks(staging string) []stagedCheck {
	t := func(name string) string { return stagingTable(staging, name) }

	scopeQuery := "SELECT DISTINCT x.scope FROM (" +
		"SELECT scope FROM " + t("notification_sinks") +
		" UNION SELECT scope FROM " + t("notification_sink_secrets") +
		" UNION SELECT scope FROM " + t("notification_sink_refs") +
		" UNION SELECT scope FROM " + t("notifications") +
		" UNION SELECT scope FROM " + t("notification_sequences") +
		" UNION SELECT scope FROM " + t("notification_cursors") +
		" UNION SELECT scope FROM " + t("notification_subscriptions") +
		" UNION SELECT scope FROM " + t("notification_deliveries") +
		") AS x WHERE x.scope <> '@server' AND NOT EXISTS (SELECT 1 FROM " + t("namespaces") +
		" AS n WHERE n.name = x.scope) LIMIT 1"

	sequenceQuery := "SELECT n.scope FROM " + t("notifications") + " AS n LEFT JOIN " + t("notification_sequences") +
		" AS s ON s.scope = n.scope WHERE s.last_sequence IS NULL OR n.sequence > s.last_sequence LIMIT 1"

	return []stagedCheck{
		{scopeQuery, "a scope that names no namespace of the backup"},
		{sequenceQuery, "a notification above the last sequence of its scope"},
		{
			"SELECT c.name FROM " + t("notification_cursors") + " AS c LEFT JOIN " + t("notification_sequences") +
				" AS s ON s.scope = c.scope WHERE c.sequence > COALESCE(s.last_sequence, 0) LIMIT 1",
			"a cursor above the last sequence of its scope",
		},
		{
			"SELECT s.ref FROM " + t("notification_sink_secrets") + " AS s LEFT JOIN " + t("notification_sinks") +
				" AS k ON k.id = s.sink_id WHERE k.id IS NULL OR k.scope <> s.scope LIMIT 1",
			"a sink secret whose sink is missing or in another scope",
		},
		{
			"SELECT s.id FROM " + t("notification_subscriptions") + " AS s LEFT JOIN " + t("notification_sinks") +
				" AS k ON k.id = s.sink_id WHERE k.id IS NULL OR k.scope <> s.scope LIMIT 1",
			"a subscription whose sink is missing or in another scope",
		},
		{
			"SELECT d.id FROM " + t("notification_deliveries") + " AS d JOIN " + t("notification_sinks") +
				" AS k ON k.id = d.sink_id WHERE k.scope <> d.scope LIMIT 1",
			"a delivery that names a sink of another scope",
		},
		{
			"SELECT d.id FROM " + t("notification_deliveries") + " AS d JOIN " + t("notification_subscriptions") +
				" AS s ON s.id = d.subscription_id WHERE s.scope <> d.scope LIMIT 1",
			"a delivery that names a subscription of another scope",
		},
		{
			"SELECT d.id FROM " + t("notification_deliveries") + " AS d JOIN " + t("notifications") +
				" AS n ON n.id = d.notification_id WHERE n.scope <> d.scope LIMIT 1",
			"a delivery that names a notification of another scope",
		},
	}
}

func checkStagedNotificationRows(ctx context.Context, pool pgQuerier, staging string) error {
	return runStaged[notifystore.NotificationRow](
		ctx,
		pool,
		"notification",
		"SELECT scope, sequence, id, type, object, object_id, subject, agent, severity, summary, links, attributes, recipients, dedupe_key, at_ms FROM "+stagingTable(
			staging,
			"notifications",
		),
	)
}

func checkStagedSubscriptionRows(ctx context.Context, pool pgQuerier, staging string) error {
	return runStaged[notifystore.SubscriptionRow](
		ctx,
		pool,
		"subscription",
		"SELECT id, scope, sink_id, types, objects, min_severity, state, reason, created_by, version FROM "+stagingTable(
			staging,
			"notification_subscriptions",
		),
	)
}

func checkStagedDeliveryRows(ctx context.Context, pool pgQuerier, staging string) error {
	return runStaged[notifystore.DeliveryRow](
		ctx,
		pool,
		"delivery",
		"SELECT id, scope, notification_id, subscription_id, sink_id, contact, principal, state, attempts, created_at_ms, next_at_ms, last_error, delivered_at_ms, owner, lease_until_ms, version FROM "+stagingTable(
			staging,
			"notification_deliveries",
		),
	)
}

// runStaged checks every row that the query reads, as a row of the type R.
func runStaged[R any, P interface {
	*R
	notifystore.StagedRow
}](ctx context.Context, pool pgQuerier, what, query string) error {
	return eachStaged(ctx, pool, stagedQuery{what: what, sql: query}, func(rows pgx.Rows) error {
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
func eachStaged(ctx context.Context, pool pgQuerier, q stagedQuery, check func(rows pgx.Rows) error) error {
	rows, err := pool.Query(ctx, q.sql)
	if err != nil {
		return fmt.Errorf("read staged %s: %w", q.what, backup.ErrExportRead)
	}

	defer rows.Close()

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
