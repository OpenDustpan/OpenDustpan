package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/backup"
)

// ApplyStagingKeepingAccess implements backup.AccessKeepingApplier: it keeps
// the current rows of the access tables, replaces the other tables with the
// staged backup, and swaps the schemas in one transaction. A backup that
// names a session of a principal the current access tables do not hold
// reports ErrRestoreAccessMismatch and changes nothing.
func (s *Store) ApplyStagingKeepingAccess(ctx context.Context) error {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()

	if s.stagingSchema == "" {
		return backup.ErrCorrupt
	}

	staging := s.stagingSchema

	if err := s.swapStagingSchemaKeepingAccess(ctx, staging); err != nil {
		return err
	}

	s.stagingSchema = ""

	return nil
}

// swapStagingSchemaKeepingAccess copies the live access tables over the
// staged ones, checks that every staged session names a live principal, and
// swaps the schemas in one transaction. Principals merge by ID so sessions
// of live principals keep their parent.
func (s *Store) swapStagingSchemaKeepingAccess(ctx context.Context, staging string) error {
	if !hasStagingPrefix(staging) {
		return fmt.Errorf("apply staging schema %q: %w", staging, backup.ErrCorrupt)
	}

	opts := pgx.TxOptions{
		IsoLevel:       pgx.ReadCommitted,
		AccessMode:     pgx.ReadWrite,
		DeferrableMode: pgx.NotDeferrable,
		BeginQuery:     "",
		CommitQuery:    "",
	}

	return inBackupTx(ctx, s.pool, opts, func(tx pgx.Tx) error {
		var live string

		if err := tx.QueryRow(ctx, "SELECT current_schema()").Scan(&live); err != nil {
			return fmt.Errorf("read live schema: %w", err)
		}

		if live == "" || live == staging {
			return fmt.Errorf("apply staging schema %q over %q: %w", staging, live, backup.ErrCorrupt)
		}

		if err := keepAccessTables(ctx, tx, live, staging); err != nil {
			return err
		}

		previous := live + "_previous"

		if _, err := tx.Exec(ctx, "ALTER SCHEMA "+pgIdent(live)+" RENAME TO "+pgIdent(previous)); err != nil {
			return fmt.Errorf("move live schema aside: %w", err)
		}

		if _, err := tx.Exec(ctx, "ALTER SCHEMA "+pgIdent(staging)+" RENAME TO "+pgIdent(live)); err != nil {
			return fmt.Errorf("move staged schema live: %w", err)
		}

		if _, err := tx.Exec(ctx, "DROP SCHEMA "+pgIdent(previous)+" CASCADE"); err != nil {
			return fmt.Errorf("drop previous schema: %w", err)
		}

		return fenceRestored(ctx, tx, live)
	})
}

// keepAccessTables replaces the access tables of staging with the live rows.
// Sessions stay as the backup holds them.
func keepAccessTables(ctx context.Context, tx pgx.Tx, live, staging string) error {
	if err := checkSessionsHavePrincipals(ctx, tx, live, staging); err != nil {
		return err
	}

	for _, table := range []string{"group_members", "access_tokens", "grants", "roles"} {
		if _, err := tx.Exec(ctx, "DELETE FROM "+pgIdent(staging)+"."+pgIdent(table)); err != nil {
			return fmt.Errorf("clear staged access table %q: %w", table, backup.ErrExportRead)
		}
	}

	if _, err := tx.Exec(ctx, "DELETE FROM "+pgIdent(staging)+"."+pgIdent("principal_groups")); err != nil {
		return fmt.Errorf("clear staged access table %q: %w", "principal_groups", backup.ErrExportRead)
	}

	if _, err := tx.Exec(ctx,
		"INSERT INTO "+pgIdent(staging)+"."+pgIdent("principal_groups")+
			" SELECT * FROM "+pgIdent(live)+"."+pgIdent("principal_groups"),
	); err != nil {
		return keepAccessError("keep staged groups", err)
	}

	if err := mergePrincipals(ctx, tx, live, staging); err != nil {
		return err
	}

	for _, table := range []string{"group_members", "access_tokens", "grants", "roles"} {
		if _, err := tx.Exec(ctx,
			"INSERT INTO "+pgIdent(staging)+"."+pgIdent(table)+
				" SELECT * FROM "+pgIdent(live)+"."+pgIdent(table),
		); err != nil {
			return keepAccessError("keep staged access table "+table, err)
		}
	}

	return nil
}

// checkSessionsHavePrincipals reports ErrRestoreAccessMismatch when the
// staged backup holds a session of a principal the live access tables do not
// hold.
func checkSessionsHavePrincipals(ctx context.Context, tx pgx.Tx, live, staging string) error {
	var principalID string

	err := tx.QueryRow(ctx,
		"SELECT s.principal_id FROM "+pgIdent(staging)+".sessions s"+
			" LEFT JOIN "+pgIdent(live)+`.principals p ON p.id = s.principal_id`+
			" WHERE p.id IS NULL LIMIT 1",
	).Scan(&principalID)
	if err == nil {
		return fmt.Errorf(
			"staged session names unknown principal %q: %w",
			principalID,
			backup.ErrRestoreAccessMismatch,
		)
	}

	if err != nil && !isNoRows(err) {
		return fmt.Errorf("check staged sessions: %w", backup.ErrExportRead)
	}

	return nil
}

// mergePrincipals adds the live principals the staging schema misses,
// updates the ones both hold, and deletes the extra ones. The children are
// already gone and the sessions name only live principals, so every delete
// keeps the foreign keys.
func mergePrincipals(ctx context.Context, tx pgx.Tx, live, staging string) error {
	columns, err := tableColumns(ctx, tx, live, "principals")
	if err != nil {
		return err
	}

	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted = append(quoted, pgIdent(column))
	}

	if _, err := tx.Exec(ctx,
		"INSERT INTO "+pgIdent(staging)+".principals ("+strings.Join(quoted, ", ")+")"+
			" SELECT "+strings.Join(qualifiedColumns(live, columns), ", ")+
			" FROM "+pgIdent(live)+".principals"+
			" WHERE id NOT IN (SELECT id FROM "+pgIdent(staging)+".principals)",
	); err != nil {
		return keepAccessError("keep staged principals", err)
	}

	if err := updateExistingPrincipals(ctx, tx, live, staging, columns); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx,
		"DELETE FROM "+pgIdent(staging)+".principals"+
			" WHERE id NOT IN (SELECT id FROM "+pgIdent(live)+".principals)",
	); err != nil {
		return keepAccessError("clear extra staged principals", err)
	}

	return nil
}

// updateExistingPrincipals writes the live values over the staged principals
// with the same ID.
func updateExistingPrincipals(
	ctx context.Context, tx pgx.Tx, live, staging string, columns []string,
) error {
	sets := make([]string, 0, len(columns))

	for _, column := range columns {
		if column == "id" {
			continue
		}

		sets = append(sets, pgIdent(column)+" = "+pgIdent("live")+ "."+pgIdent(column))
	}

	if len(sets) == 0 {
		return nil
	}

	if _, err := tx.Exec(ctx,
		"UPDATE "+pgIdent(staging)+".principals AS staged SET "+strings.Join(sets, ", ")+
			" FROM "+pgIdent(live)+".principals AS live WHERE staged.id = live.id",
	); err != nil {
		return keepAccessError("keep staged principals", err)
	}

	return nil
}

// tableColumns returns the column names of table in schema in ordinal order.
func tableColumns(ctx context.Context, tx pgx.Tx, schema, table string) ([]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT column_name FROM information_schema.columns`+
			` WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position`,
		schema, table,
	)
	if err != nil {
		return nil, fmt.Errorf("read columns of table %q: %w", table, backup.ErrExportRead)
	}

	defer rows.Close()

	var out []string

	for rows.Next() {
		var name string

		if scanErr := rows.Scan(&name); scanErr != nil {
			return nil, fmt.Errorf("read columns of table %q: %w", table, backup.ErrExportRead)
		}

		out = append(out, name)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read columns of table %q: %w", table, backup.ErrExportRead)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("read columns of table %q: %w", table, backup.ErrExportRead)
	}

	return out, nil
}

// qualifiedColumns answers live.column for every column.
func qualifiedColumns(schema string, columns []string) []string {
	out := make([]string, 0, len(columns))

	for _, column := range columns {
		out = append(out, pgIdent(schema)+"."+pgIdent(column))
	}

	return out
}

// keepAccessError maps a foreign key failure while keeping the access tables
// to ErrRestoreAccessMismatch, so the restore reports the mismatch instead
// of driver text. Any other failure keeps its cause.
func keepAccessError(op string, err error) error {
	if err == nil {
		return nil //nolint:nilnil // No failure needs no error.
	}

	if isForeignKeyError(err) {
		return fmt.Errorf("%s: %w", op, backup.ErrRestoreAccessMismatch)
	}

	return fmt.Errorf("%s: %w", op, err)
}

// isForeignKeyError reports whether err is a foreign key violation.
func isForeignKeyError(err error) bool {
	if err == nil {
		return false
	}

	return strings.Contains(err.Error(), "violates foreign key") ||
		strings.Contains(err.Error(), "foreign key constraint")
}

// isNoRows reports whether err is the missing-row error of pgx.
func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
