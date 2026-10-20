package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/backup"
)

// ApplyStagingKeepingAccess implements backup.AccessKeepingApplier: it keeps
// the current rows of the access tables, replaces the other tables with the
// staged backup, and swaps the schemas in one transaction. A kept grant or
// principal that names a namespace or a unit the staged backup does not hold
// reports ErrRestoreAccessMismatch and changes nothing.
func (s *Store) ApplyStagingKeepingAccess(ctx context.Context) (backup.KeepAccessResult, error) {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()

	if s.stagingSchema == "" {
		return backup.KeepAccessResult{}, backup.ErrCorrupt
	}

	staging := s.stagingSchema

	result, err := s.swapStagingSchemaKeepingAccess(ctx, staging)
	if err != nil {
		return backup.KeepAccessResult{}, err
	}

	s.stagingSchema = ""

	return result, nil
}

// swapStagingSchemaKeepingAccess copies the live access tables over the
// staged ones, checks the kept grants and principals against the staged
// namespaces and units, and swaps the schemas in one transaction.
// Principals merge by ID: existing ones are updated, extra ones are deleted,
// and missing ones are inserted last. The transaction locks the live access
// tables first, so a revoke between the copy and the swap cannot be lost.
func (s *Store) swapStagingSchemaKeepingAccess(
	ctx context.Context, staging string,
) (backup.KeepAccessResult, error) {
	var result backup.KeepAccessResult

	if !hasStagingPrefix(staging) {
		return result, fmt.Errorf("apply staging schema %q: %w", staging, backup.ErrCorrupt)
	}

	opts := pgx.TxOptions{
		IsoLevel:       pgx.ReadCommitted,
		AccessMode:     pgx.ReadWrite,
		DeferrableMode: pgx.NotDeferrable,
		BeginQuery:     "",
		CommitQuery:    "",
	}

	err := inBackupTx(ctx, s.pool, opts, func(tx pgx.Tx) error {
		dropped, txErr := s.keepAccessTx(ctx, tx, staging)
		result.DroppedTempAdmins = dropped

		return txErr
	})

	return result, err
}

// keepAccessTx runs the keep-access swap in tx and returns the count of
// dropped temp_admins rows.
func (s *Store) keepAccessTx(ctx context.Context, tx pgx.Tx, staging string) (int64, error) {
	var live string

	if err := tx.QueryRow(ctx, "SELECT current_schema()").Scan(&live); err != nil {
		return 0, fmt.Errorf("read live schema: %w", err)
	}

	if live == "" || live == staging {
		return 0, fmt.Errorf("apply staging schema %q over %q: %w", staging, live, backup.ErrCorrupt)
	}

	// The check covers the whole staging area, also the access tables that
	// the swap does not take, and runs before any write.
	if err := checkSwapDev(ctx, tx, staging); err != nil {
		return 0, err
	}

	if err := lockLiveAccessTables(ctx, tx, live); err != nil {
		return 0, err
	}

	if s.keepAccessHook != nil {
		s.keepAccessHook(ctx, tx, live)
	}

	if err := keepAccessTables(ctx, tx, live, staging); err != nil {
		return 0, err
	}

	// The swap takes temp_admins from the backup and keeps the live
	// principals, which keepAccessTables put into the staged schema. So a
	// staged row can name a principal that the live principals table does
	// not hold.
	dropped, err := dropDanglingTempAdmins(ctx, tx, staging)
	if err != nil {
		return 0, err
	}

	return dropped, swapSchemas(ctx, tx, live, staging)
}

// swapSchemas moves the live schema aside, moves the staged schema live,
// drops the old live schema and the control schema of the staging area, and
// fences the restored namespaces, in tx. The installation, backups and
// restores tables live in the control schema, which the swap never renames.
func swapSchemas(ctx context.Context, tx pgx.Tx, live, staging string) error {
	previous := live + "_previous"

	if _, err := tx.Exec(ctx, "ALTER SCHEMA "+pgIdent(live)+" RENAME TO "+pgIdent(previous)); err != nil {
		return fmt.Errorf("move live schema aside: %w", err)
	}

	if _, err := tx.Exec(ctx, "ALTER SCHEMA "+pgIdent(staging)+" RENAME TO "+pgIdent(live)); err != nil {
		return fmt.Errorf("move staged schema live: %w", err)
	}

	if err := keepControlTables(ctx, tx, live, previous); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, "DROP SCHEMA "+pgIdent(previous)+" CASCADE"); err != nil {
		return fmt.Errorf("drop previous schema: %w", err)
	}

	if _, err := tx.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgIdent(staging+"_control")+" CASCADE"); err != nil {
		return fmt.Errorf("drop staging control schema: %w", err)
	}

	return fenceRestored(ctx, tx, live)
}

// lockLiveAccessTables locks every live access table in share mode, so no
// concurrent revoke or grant lands between the live snapshot and the swap.
func lockLiveAccessTables(ctx context.Context, tx pgx.Tx, live string) error {
	tables := make([]string, 0, len(backup.AccessTables))
	for _, table := range backup.AccessTables {
		tables = append(tables, pgIdent(live)+"."+pgIdent(table))
	}

	query := "LOCK TABLE " + strings.Join(tables, ", ") + " IN SHARE MODE"
	if _, err := tx.Exec(ctx, query); err != nil {
		return fmt.Errorf("lock live access tables: %w", backup.ErrExportRead)
	}

	return nil
}

// keepAccessTables replaces the access tables of staging with the live rows.
// Before any write it checks that every kept grant and every kept
// namespace-scoped principal names a namespace and a unit the staged backup
// holds: a mismatch reports ErrRestoreAccessMismatch and changes nothing.
func keepAccessTables(ctx context.Context, tx pgx.Tx, live, staging string) error {
	if err := checkKeptAccessReferences(ctx, tx, live, staging); err != nil {
		return err
	}

	for _, table := range keepCopyTables() {
		clearQuery := "DELETE FROM " + pgIdent(staging) + "." + pgIdent(table)
		if _, err := tx.Exec(ctx, clearQuery); err != nil {
			return fmt.Errorf("clear staged access table %q: %w", table, backup.ErrExportRead)
		}
	}

	clearGroups := "DELETE FROM " + pgIdent(staging) + "." + pgIdent(backup.TablePrincipalGroups)
	if _, err := tx.Exec(ctx, clearGroups); err != nil {
		return fmt.Errorf("clear staged access table %q: %w", backup.TablePrincipalGroups, backup.ErrExportRead)
	}

	if err := copyAccessTable(ctx, tx, live, staging, backup.TablePrincipalGroups); err != nil {
		return keepAccessError("keep staged groups", err)
	}

	// Merge the principals before the child rows are copied: each copied child
	// row names a principal, so the principal row must exist first.
	if err := mergePrincipals(ctx, tx, live, staging); err != nil {
		return err
	}

	for _, table := range keepCopyTables() {
		if err := copyAccessTable(ctx, tx, live, staging, table); err != nil {
			return keepAccessError("keep staged access table "+table, err)
		}
	}

	return nil
}

// keepCopyTables lists the access tables that a keep copies wholesale, in
// the order of backup.AccessTables: principal groups first so group members
// keep their parent, then the other children parents before children.
// Principals merge by ID in mergePrincipals instead, so neither list names
// them. The one list decides for both stores: dropping a table from
// backup.AccessTables drops it from the keep on both dialects.
func keepCopyTables() []string {
	out := make([]string, 0, len(backup.AccessTables))

	for _, table := range backup.AccessTables {
		if table == backup.TablePrincipals || table == backup.TablePrincipalGroups {
			continue
		}

		out = append(out, table)
	}

	return out
}

// copyAccessTable copies every row of table from the live schema into the
// staged schema, naming every column so no query selects star. The source
// table carries the alias src, and every selected column names it: a
// schema-qualified column (live.col) is not valid SQL.
func copyAccessTable(ctx context.Context, tx pgx.Tx, live, staging, table string) error {
	columns, err := tableColumns(ctx, tx, live, table)
	if err != nil {
		return err
	}

	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted = append(quoted, pgIdent(column))
	}

	from := make([]string, 0, len(columns))
	for _, column := range columns {
		from = append(from, pgIdent("src")+"."+pgIdent(column))
	}

	query := "INSERT INTO " + pgIdent(staging) + "." + pgIdent(table) +
		" (" + strings.Join(quoted, ", ") + ")" +
		" SELECT " + strings.Join(from, ", ") +
		" FROM " + pgIdent(live) + "." + pgIdent(table) + " AS src"
	if _, err := tx.Exec(ctx, query); err != nil {
		return keepAccessError("copy access table "+table, err)
	}

	return nil
}

// checkKeptAccessReferences reports ErrRestoreAccessMismatch when a kept
// current grant names a namespace or a unit the staged backup does not hold,
// or when a kept namespace-scoped principal names a namespace the staged
// backup does not hold. Only current grants and principals count: revoked
// grants and deleted principals are history and need no target.
func checkKeptAccessReferences(ctx context.Context, tx pgx.Tx, live, staging string) error {
	stagedNS, err := stringSet(ctx, tx, "namespaces", "SELECT "+pgIdent("name")+" FROM "+pgIdent(staging)+".namespaces")
	if err != nil {
		return err
	}

	stacks, err := stringSet(ctx, tx, "stacks", "SELECT "+pgIdent("id")+" FROM "+pgIdent(staging)+".stacks")
	if err != nil {
		return err
	}

	workspaces, err := stringSet(ctx, tx, "workspaces", "SELECT "+pgIdent("id")+" FROM "+pgIdent(staging)+".workspaces")
	if err != nil {
		return err
	}

	stagedUnits := map[string]bool{}
	for id := range stacks {
		stagedUnits[id] = true
	}

	for id := range workspaces {
		stagedUnits[id] = true
	}

	if err := checkKeptGrants(ctx, tx, live, stagedNS, stagedUnits); err != nil {
		return err
	}

	return checkKeptPrincipals(ctx, tx, live, stagedNS)
}

// stringSet returns the distinct text values of a one-column query over the
// staged what. A failure maps to ErrExportRead without driver text.
func stringSet(ctx context.Context, tx pgx.Tx, what, query string) (map[string]bool, error) {
	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read staged %s: %w", what, backup.ErrExportRead)
	}

	defer rows.Close()

	out := map[string]bool{}

	for rows.Next() {
		var value string

		if scanErr := rows.Scan(&value); scanErr != nil {
			return nil, fmt.Errorf("read staged %s: %w", what, backup.ErrExportRead)
		}

		out[value] = true
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read staged %s: %w", what, backup.ErrExportRead)
	}

	return out, nil
}

// checkKeptGrants reports ErrRestoreAccessMismatch when a current kept grant
// names a namespace or a unit the staged backup does not hold.
func checkKeptGrants(
	ctx context.Context, tx pgx.Tx, live string, stagedNS, stagedUnits map[string]bool,
) error {
	rows, err := tx.Query(ctx,
		"SELECT "+pgIdent("id")+", "+pgIdent("namespace")+", "+pgIdent("target")+
			" FROM "+pgIdent(live)+".grants WHERE revoked_at_ms IS NULL")
	if err != nil {
		return fmt.Errorf("check kept grants: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var id, ns, target string

		if scanErr := rows.Scan(&id, &ns, &target); scanErr != nil {
			return fmt.Errorf("check kept grants: %w", backup.ErrExportRead)
		}

		if ns != "" && !stagedNS[ns] {
			return fmt.Errorf(
				"kept grant %q names missing namespace %q: %w",
				id,
				ns,
				backup.ErrRestoreAccessMismatch,
			)
		}

		if unitID, ok := strings.CutPrefix(target, "unit:"); ok && !stagedUnits[unitID] {
			return fmt.Errorf(
				"kept grant %q names missing unit %q: %w",
				id,
				target,
				backup.ErrRestoreAccessMismatch,
			)
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("check kept grants: %w", backup.ErrExportRead)
	}

	return nil
}

// checkKeptPrincipals reports ErrRestoreAccessMismatch when a current kept
// namespace-scoped principal names a namespace the staged backup does not
// hold.
func checkKeptPrincipals(
	ctx context.Context, tx pgx.Tx, live string, stagedNS map[string]bool,
) error {
	rows, err := tx.Query(ctx,
		"SELECT "+pgIdent("id")+", "+pgIdent("namespace")+
			" FROM "+pgIdent(live)+".principals WHERE deleted_at_ms IS NULL")
	if err != nil {
		return fmt.Errorf("check kept principals: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var id, ns string

		if scanErr := rows.Scan(&id, &ns); scanErr != nil {
			return fmt.Errorf("check kept principals: %w", backup.ErrExportRead)
		}

		if ns != "" && !stagedNS[ns] {
			return fmt.Errorf(
				"kept principal %q names missing namespace %q: %w",
				id,
				ns,
				backup.ErrRestoreAccessMismatch,
			)
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("check kept principals: %w", backup.ErrExportRead)
	}

	return nil
}

// mergePrincipals updates the staged principals the live schema also holds,
// deletes the staged ones the live schema does not hold, and then inserts
// the live ones the staging schema misses. The children are already gone,
// so every delete keeps the foreign keys. Update and delete run before the
// insert, so a principal deleted and created again with the same subject
// does not hit the subject unique index.
func mergePrincipals(ctx context.Context, tx pgx.Tx, live, staging string) error {
	columns, err := tableColumns(ctx, tx, live, backup.TablePrincipals)
	if err != nil {
		return err
	}

	if err := updateExistingPrincipals(ctx, tx, live, staging, columns); err != nil {
		return err
	}

	deleteQuery := "DELETE FROM " + pgIdent(staging) + "." + pgIdent(backup.TablePrincipals) +
		" WHERE id NOT IN (SELECT id FROM " + pgIdent(live) + "." + pgIdent(backup.TablePrincipals) + ")"
	if _, err := tx.Exec(ctx, deleteQuery); err != nil {
		return keepAccessError("clear extra staged principals", err)
	}

	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted = append(quoted, pgIdent(column))
	}

	insertQuery := "INSERT INTO " + pgIdent(staging) + "." + pgIdent(backup.TablePrincipals) +
		" (" + strings.Join(quoted, ", ") + ")" +
		" SELECT " + strings.Join(qualifiedColumns("src", columns), ", ") +
		" FROM " + pgIdent(live) + "." + pgIdent(backup.TablePrincipals) + " AS src" +
		" WHERE id NOT IN (SELECT id FROM " + pgIdent(staging) + "." + pgIdent(backup.TablePrincipals) + ")"
	if _, err := tx.Exec(ctx, insertQuery); err != nil {
		return keepAccessError("keep staged principals", err)
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

		sets = append(sets, pgIdent(column)+" = "+pgIdent("live")+"."+pgIdent(column))
	}

	if len(sets) == 0 {
		return nil
	}

	updateQuery := "UPDATE " + pgIdent(staging) + "." + pgIdent(backup.TablePrincipals) +
		" AS staged SET " + strings.Join(sets, ", ") +
		" FROM " + pgIdent(live) + "." + pgIdent(backup.TablePrincipals) + " AS live WHERE staged.id = live.id"
	if _, err := tx.Exec(ctx, updateQuery); err != nil {
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

// qualifiedColumns answers alias.column for every column.
func qualifiedColumns(alias string, columns []string) []string {
	out := make([]string, 0, len(columns))

	for _, column := range columns {
		out = append(out, pgIdent(alias)+"."+pgIdent(column))
	}

	return out
}

// keepAccessError maps a foreign key failure while keeping the access tables
// to ErrRestoreAccessMismatch, so the restore reports the mismatch instead
// of driver text. Any other failure keeps its cause.
func keepAccessError(op string, err error) error {
	if err == nil {
		return nil
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
