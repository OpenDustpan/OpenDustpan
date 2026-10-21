package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/githoststore"
)

// Columns that more than one Git host table holds, or another table too.
const (
	backupColInstallationID = "installation_id"
	backupColKeyCreatedAtMs = "key_created_at_ms"
)

// sqliteGitHostBackupTables holds the backup columns of the Git host tables,
// in the order of backup.Tables.
func sqliteGitHostBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: backup.TableGitHostAppRegistrations,
			columns: []string{
				backupColKind,
				"app_id",
				"base_url",
				"hook_id",
				"sealed_key",
				"key_set_id",
				backupColKeyCreatedAtMs,
				"sealed_event_secret",
				"event_secret_set_id",
				"event_secret_created_at_ms",
				"address_digest",
				backupColVersion,
				backupColCreatedAtMs,
				backupColUpdatedAtMs,
			},
		},
		{
			table: "git_host_installations",
			columns: []string{
				backupColInstallationID,
				backupColNamespaceID,
				backupColVersion,
				backupColCreatedAtMs,
				backupColUpdatedAtMs,
			},
		},
		{
			table: "git_host_connections",
			columns: []string{
				backupColNamespaceID,
				"id",
				backupColKind,
				"auth",
				"app_scope",
				backupColInstallationID,
				"push",
				"is_default",
				backupColVersion,
				backupColCreatedAtMs,
				backupColUpdatedAtMs,
			},
		},
	}
}

// checkStagedGitHost checks each Git host row of a staged backup with the rules
// of a new row. A row that fails makes the backup corrupt, so a restore never
// brings in a registration, an assignment or a connection that the server would
// not have stored. It returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedGitHost(ctx context.Context, conn *sql.DB) error {
	if err := checkStagedRegistrations(ctx, conn); err != nil {
		return err
	}

	if err := checkStagedAssignments(ctx, conn); err != nil {
		return err
	}

	return checkStagedConnections(ctx, conn)
}

func checkStagedRegistrations(ctx context.Context, conn *sql.DB) error {
	const query = `SELECT kind, app_id, base_url, hook_id, sealed_key, key_set_id, key_created_at_ms,
sealed_event_secret, event_secret_set_id, event_secret_created_at_ms, address_digest, version, created_at_ms,
updated_at_ms FROM git_host_app_registrations`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged Git host registrations: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	for rows.Next() {
		var row githoststore.RegistrationRow

		scanErr := rows.Scan(
			&row.Kind, &row.AppID, &row.BaseURL, &row.HookID, &row.SealedKey, &row.KeySetID, &row.KeyCreatedAtMs,
			&row.SealedEventSecret, &row.EventSecretSetID, &row.EventSecretCreatedAtMs, &row.AddressDigest,
			&row.Version, &row.CreatedAtMs, &row.UpdatedAtMs,
		)
		if scanErr != nil {
			return fmt.Errorf("read staged Git host registrations: %w", backup.ErrExportRead)
		}

		if _, regErr := githoststore.Registration(row); regErr != nil {
			return fmt.Errorf(
				"staged Git host registration %q is not valid: %w: %w",
				row.Kind,
				regErr,
				backup.ErrCorrupt,
			)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged Git host registrations: %w", backup.ErrExportRead)
	}

	return nil
}

func checkStagedAssignments(ctx context.Context, conn *sql.DB) error {
	const query = `SELECT i.installation_id, n.name, i.version, i.created_at_ms, i.updated_at_ms
FROM git_host_installations AS i LEFT JOIN namespaces AS n ON n.id = i.namespace_id`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged Git host assignments: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	for rows.Next() {
		var (
			row       githoststore.AssignmentRow
			namespace sql.NullString
		)

		scanErr := rows.Scan(
			&row.InstallationID, &namespace, &row.Version, &row.CreatedAtMs, &row.UpdatedAtMs,
		)
		if scanErr != nil {
			return fmt.Errorf("read staged Git host assignments: %w", backup.ErrExportRead)
		}

		// An assignment to a namespace that the backup does not hold is corrupt.
		// The LEFT JOIN keeps such a row in the result, and the NULL name fails it.
		if !namespace.Valid {
			return fmt.Errorf(
				"staged Git host assignment %q has no namespace: %w",
				row.InstallationID,
				backup.ErrCorrupt,
			)
		}

		row.Namespace = namespace.String

		if _, aErr := githoststore.Assignment(row); aErr != nil {
			return fmt.Errorf(
				"staged Git host assignment %q is not valid: %w: %w",
				row.InstallationID,
				aErr,
				backup.ErrCorrupt,
			)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged Git host assignments: %w", backup.ErrExportRead)
	}

	return nil
}

func checkStagedConnections(ctx context.Context, conn *sql.DB) error {
	const query = `SELECT n.name, c.id, c.kind, c.auth, c.app_scope, c.installation_id, c.push, c.is_default,
c.version, c.created_at_ms, c.updated_at_ms
FROM git_host_connections AS c LEFT JOIN namespaces AS n ON n.id = c.namespace_id`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged Git host connections: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	for rows.Next() {
		var (
			row       githoststore.ConnectionRow
			namespace sql.NullString
		)

		scanErr := rows.Scan(
			&namespace, &row.ID, &row.Kind, &row.Auth, &row.AppScope, &row.InstallationID, &row.Push,
			&row.IsDefault, &row.Version, &row.CreatedAtMs, &row.UpdatedAtMs,
		)
		if scanErr != nil {
			return fmt.Errorf("read staged Git host connections: %w", backup.ErrExportRead)
		}

		if !namespace.Valid {
			return fmt.Errorf("staged Git host connection %q has no namespace: %w", row.ID, backup.ErrCorrupt)
		}

		row.Namespace = namespace.String

		if _, cErr := githoststore.Connection(row); cErr != nil {
			return fmt.Errorf("staged Git host connection %q is not valid: %w: %w", row.ID, cErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged Git host connections: %w", backup.ErrExportRead)
	}

	return nil
}
