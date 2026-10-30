package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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
func checkStagedNotifications(ctx context.Context, pool *pgxpool.Pool, staging string) error {
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

	for _, check := range []struct {
		query string
		what  string
	}{
		{scopeQuery, "a scope that names no namespace of the backup"},
		{sequenceQuery, "a notification above the last sequence of its scope"},
	} {
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

func checkStagedNotificationRows(ctx context.Context, pool *pgxpool.Pool, staging string) error {
	rows, err := pool.Query(ctx, `SELECT scope, sequence, id, type, object, object_id, subject, agent, severity,
summary, links, attributes, recipients, dedupe_key, at_ms FROM `+stagingTable(staging, "notifications"))
	if err != nil {
		return fmt.Errorf("read staged notifications: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var r notifystore.NotificationRow

		if scanErr := rows.Scan(&r.Scope, &r.Sequence, &r.ID, &r.Type, &r.Object, &r.ObjectID, &r.Subject, &r.Agent,
			&r.Severity, &r.Summary, &r.Links, &r.Attributes, &r.Recipients, &r.DedupeKey, &r.AtMs); scanErr != nil {
			return fmt.Errorf("read staged notifications: %w", backup.ErrExportRead)
		}

		if _, convErr := notifystore.Notification(r); convErr != nil {
			return fmt.Errorf("staged notification %q: %w: %w", r.ID, convErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged notifications: %w", backup.ErrExportRead)
	}

	return nil
}

func checkStagedSubscriptionRows(ctx context.Context, pool *pgxpool.Pool, staging string) error {
	rows, err := pool.Query(ctx, `SELECT id, scope, sink_id, types, objects, min_severity, state, reason,
created_by, version FROM `+stagingTable(staging, "notification_subscriptions"))
	if err != nil {
		return fmt.Errorf("read staged subscriptions: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var r notifystore.SubscriptionRow

		if scanErr := rows.Scan(&r.ID, &r.Scope, &r.SinkID, &r.Types, &r.Objects, &r.MinSeverity, &r.State,
			&r.Reason, &r.CreatedBy, &r.Version); scanErr != nil {
			return fmt.Errorf("read staged subscriptions: %w", backup.ErrExportRead)
		}

		if _, convErr := notifystore.Subscription(r); convErr != nil {
			return fmt.Errorf("staged subscription %q: %w: %w", r.ID, convErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged subscriptions: %w", backup.ErrExportRead)
	}

	return nil
}

func checkStagedDeliveryRows(ctx context.Context, pool *pgxpool.Pool, staging string) error {
	rows, err := pool.Query(ctx, `SELECT id, scope, notification_id, subscription_id, sink_id, contact,
principal, state, attempts, next_at_ms, last_error, delivered_at_ms, owner, lease_until_ms, version FROM `+
		stagingTable(staging, "notification_deliveries"))
	if err != nil {
		return fmt.Errorf("read staged deliveries: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var r notifystore.DeliveryRow

		if scanErr := rows.Scan(&r.ID, &r.Scope, &r.NotificationID, &r.SubscriptionID, &r.SinkID, &r.Contact,
			&r.Principal, &r.State, &r.Attempts, &r.NextAtMs, &r.LastError, &r.DeliveredAtMs, &r.Owner,
			&r.LeaseUntilMs, &r.Version); scanErr != nil {
			return fmt.Errorf("read staged deliveries: %w", backup.ErrExportRead)
		}

		if _, convErr := notifystore.Delivery(r); convErr != nil {
			return fmt.Errorf("staged delivery %q: %w: %w", r.ID, convErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged deliveries: %w", backup.ErrExportRead)
	}

	return nil
}
