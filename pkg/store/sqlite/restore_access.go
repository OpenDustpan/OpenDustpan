package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/backup"
)

// ApplyStagingKeepingAccess implements backup.AccessKeepingApplier: it keeps
// the current rows of the access tables, replaces the other tables with the
// staged backup, fences every namespace, and swaps the staged file live. The
// staged file passed every check in StagePortable. A kept grant or principal
// that names a namespace or a unit the staged backup does not hold reports
// ErrRestoreAccessMismatch and changes nothing.
func (s *Store) ApplyStagingKeepingAccess(ctx context.Context) (backup.KeepAccessResult, error) {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()

	if s.stagingPath == "" {
		return backup.KeepAccessResult{}, backup.ErrCorrupt
	}

	staged := s.stagingPath

	// The audit chain record is bookkeeping of this installation: the
	// staged file takes the value of the live database. The control tables
	// stay across the swap with the live rows. Read the chain before the
	// write lock, because the read takes the read lock.
	keep, err := s.liveBookkeeping(ctx)
	if err != nil {
		return backup.KeepAccessResult{}, err
	}

	// Hold the write lock from the live snapshot through the swap, so a
	// revoke between the copy and the swap cannot be lost. Lock order is
	// stagingMu before mu.
	s.mu.Lock()
	defer s.mu.Unlock()

	// The check covers the whole staging area, also the access tables that
	// the swap does not take, and runs before any write to the staged file.
	if err := s.checkSwapDevLocked(ctx, staged); err != nil {
		return backup.KeepAccessResult{}, err
	}

	if err := s.keepLiveAccessTables(ctx, staged); err != nil {
		return backup.KeepAccessResult{}, err
	}

	// The swap takes temp_admins from the backup and keeps the live
	// principals, so a staged row can name a principal that the live
	// principals table does not hold.
	dropped, err := dropDanglingTempAdmins(ctx, staged)
	if err != nil {
		return backup.KeepAccessResult{}, err
	}

	if err := fenceStagedFile(ctx, staged, s.path, keep); err != nil {
		return backup.KeepAccessResult{}, err
	}

	if err := s.swapDatabaseLocked(ctx, staged); err != nil {
		return backup.KeepAccessResult{}, err
	}

	s.stagingPath = ""

	return backup.KeepAccessResult{DroppedTempAdmins: dropped}, nil
}

// keepLiveAccessTables replaces the access tables of the staged file at path
// with the current live rows. The caller holds the write lock. Before any
// write it checks that every kept grant and every kept namespace-scoped
// principal names a namespace and a unit the staged backup holds: a mismatch
// reports ErrRestoreAccessMismatch and changes nothing.
func (s *Store) keepLiveAccessTables(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("keep access tables: %w", err)
	}

	specs := accessTableSpecs()

	live, err := s.readLiveAccessTables(ctx, specs)
	if err != nil {
		return err
	}

	if s.keepAccessHook != nil {
		s.keepAccessHook(ctx)
	}

	if err := checkKeptAccessReferences(ctx, path, live); err != nil {
		return err
	}

	if err := overwriteStagedAccessTables(ctx, path, specs, live); err != nil {
		return err
	}

	return checkStagedForeignKeys(ctx, path)
}

// accessRows holds the live rows of every access table, keyed by table, with
// the SQLite column names of each table.
type accessRows struct {
	columns map[string][]string
	rows    map[string][][]any
}

// accessTableSpecs returns the portable specs of the access tables, keyed by
// table in the order of backup.AccessTables.
func accessTableSpecs() []backup.PortableTable {
	byTable := make(map[string]backup.PortableTable)

	for _, spec := range backup.PortableTables() {
		if backup.IsAccessTable(spec.Table) {
			byTable[spec.Table] = spec
		}
	}

	out := make([]backup.PortableTable, 0, len(backup.AccessTables))

	for _, name := range backup.AccessTables {
		out = append(out, byTable[name])
	}

	return out
}

// readLiveAccessTables reads every access table from the live database. The
// caller holds the write lock from the snapshot through the swap, so the rows
// come from one consistent view and no revoke lands between the copy and the
// swap.
func (s *Store) readLiveAccessTables(
	ctx context.Context, specs []backup.PortableTable,
) (accessRows, error) {
	out := accessRows{columns: map[string][]string{}, rows: map[string][][]any{}}

	for _, spec := range specs {
		columns, values, err := s.readOneAccessTable(ctx, spec)
		if err != nil {
			return accessRows{}, err
		}

		out.columns[spec.Table] = columns
		out.rows[spec.Table] = values
	}

	return out, nil
}

// readOneAccessTable reads one access table from the live database.
func (s *Store) readOneAccessTable(
	ctx context.Context, spec backup.PortableTable,
) ([]string, [][]any, error) {
	columns := make([]string, 0, len(spec.Columns))
	for _, column := range spec.Columns {
		columns = append(columns, `"`+sqliteColumnName(spec.Table, column.Name)+`"`)
	}

	query := "SELECT " + strings.Join(columns, ", ") + // #nosec G202 // Constant names from the portable spec.
		` FROM "` + spec.Table + `"`

	rows, err := s.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, nil, fmt.Errorf("read live access table %q: %w", spec.Table, backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Rows read fully; close is best effort.
	}()

	values, scanErr := scanAllRows(rows, spec.Table)
	if scanErr != nil {
		return nil, nil, scanErr
	}

	return columns, values, nil
}

// scanAllRows scans every row of rows into storage values.
func scanAllRows(rows *sql.Rows, table string) ([][]any, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("read live access table %q: %w", table, backup.ErrExportRead)
	}

	var out [][]any

	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))

		for i := range values {
			pointers[i] = &values[i]
		}

		if scanErr := rows.Scan(pointers...); scanErr != nil {
			return nil, fmt.Errorf("read live access table %q: %w", table, backup.ErrExportRead)
		}

		out = append(out, values)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read live access table %q: %w", table, backup.ErrExportRead)
	}

	return out, nil
}

// checkKeptAccessReferences reports ErrRestoreAccessMismatch when a kept
// current grant names a namespace or a unit the staged backup does not hold,
// or when a kept namespace-scoped principal names a namespace the staged
// backup does not hold. Only current grants and principals count: revoked
// grants and deleted principals are history and need no target.
func checkKeptAccessReferences(ctx context.Context, path string, live accessRows) error {
	staged, err := readStagedNamespacesAndUnits(ctx, path)
	if err != nil {
		return err
	}

	if err := checkKeptGrants(staged, live); err != nil {
		return err
	}

	return checkKeptPrincipals(staged.namespaces, live)
}

// stagedRefs holds the namespace names and unit IDs of a staged backup:
// the namespaces table and the stack and workspace IDs.
type stagedRefs struct {
	namespaces map[string]bool
	units      map[string]bool
}

// readStagedNamespacesAndUnits returns the namespace names and the unit IDs
// (stack and workspace IDs) of the staged file at path.
func readStagedNamespacesAndUnits(ctx context.Context, path string) (stagedRefs, error) {
	conn, err := openStagedAccess(ctx, path)
	if err != nil {
		return stagedRefs{}, err
	}

	defer func() {
		_ = conn.Close() //nolint:errcheck // Read-only check handle.
	}()

	namespaces, err := stringSet(ctx, conn, "namespaces", `SELECT name FROM namespaces`)
	if err != nil {
		return stagedRefs{}, err
	}

	stacks, err := stringSet(ctx, conn, "stacks", `SELECT id FROM stacks`)
	if err != nil {
		return stagedRefs{}, err
	}

	workspaces, err := stringSet(ctx, conn, "workspaces", `SELECT id FROM workspaces`)
	if err != nil {
		return stagedRefs{}, err
	}

	units := map[string]bool{}
	for id := range stacks {
		units[id] = true
	}

	for id := range workspaces {
		units[id] = true
	}

	return stagedRefs{namespaces: namespaces, units: units}, nil
}

// stringSet returns the distinct text values of a one-column query over the
// staged what. A failure maps to ErrExportRead without driver text.
func stringSet(ctx context.Context, conn *sql.DB, what, query string) (map[string]bool, error) {
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read staged %s: %w", what, backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

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

// grantColumns holds the column indexes of the kept grants check.
type grantColumns struct {
	ns      int
	target  int
	revoked int
	id      int
}

// checkKeptGrants reports ErrRestoreAccessMismatch when a current kept grant
// names a namespace or a unit the staged backup does not hold.
func checkKeptGrants(staged stagedRefs, live accessRows) error {
	columns := live.columns[backup.TableGrants]
	cols := grantColumns{
		ns:      columnIndex(columns, `"namespace"`),
		target:  columnIndex(columns, `"target"`),
		revoked: columnIndex(columns, `"revoked_at_ms"`),
		id:      columnIndex(columns, `"id"`),
	}

	if cols.ns < 0 || cols.target < 0 || cols.revoked < 0 || cols.id < 0 {
		return fmt.Errorf("check kept grants: %w", backup.ErrExportRead)
	}

	for _, row := range live.rows[backup.TableGrants] {
		if row[cols.revoked] != nil {
			continue
		}

		if err := checkOneKeptGrant(staged, cols, row); err != nil {
			return err
		}
	}

	return nil
}

// checkOneKeptGrant reports ErrRestoreAccessMismatch when one current kept
// grant names a namespace or a unit the staged backup does not hold.
func checkOneKeptGrant(staged stagedRefs, cols grantColumns, row []any) error {
	id, err := accessString(row, cols.id, backup.TableGrants, "id")
	if err != nil {
		return err
	}

	ns, err := accessString(row, cols.ns, backup.TableGrants, "namespace")
	if err != nil {
		return err
	}

	target, err := accessString(row, cols.target, backup.TableGrants, "target")
	if err != nil {
		return err
	}

	if ns != "" && !staged.namespaces[ns] {
		return fmt.Errorf(
			"kept grant %q names missing namespace %q: %w",
			id,
			ns,
			backup.ErrRestoreAccessMismatch,
		)
	}

	if unitID, ok := strings.CutPrefix(target, "unit:"); ok && !staged.units[unitID] {
		return fmt.Errorf(
			"kept grant %q names missing unit %q: %w",
			id,
			target,
			backup.ErrRestoreAccessMismatch,
		)
	}

	return nil
}

// checkKeptPrincipals reports ErrRestoreAccessMismatch when a current kept
// namespace-scoped principal names a namespace the staged backup does not
// hold.
func checkKeptPrincipals(stagedNS map[string]bool, live accessRows) error {
	columns := live.columns[backup.TablePrincipals]
	nsIdx := columnIndex(columns, `"namespace"`)
	deletedIdx := columnIndex(columns, `"deleted_at_ms"`)
	idIdx := columnIndex(columns, `"id"`)

	if nsIdx < 0 || deletedIdx < 0 || idIdx < 0 {
		return fmt.Errorf("check kept principals: %w", backup.ErrExportRead)
	}

	for _, row := range live.rows[backup.TablePrincipals] {
		if row[deletedIdx] != nil {
			continue
		}

		id, err := accessString(row, idIdx, backup.TablePrincipals, "id")
		if err != nil {
			return err
		}

		ns, err := accessString(row, nsIdx, backup.TablePrincipals, "namespace")
		if err != nil {
			return err
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

	return nil
}

// accessString returns the text of the NOT NULL column at i of a live
// access row. A value of another type reports ErrExportRead.
func accessString(row []any, i int, table, column string) (string, error) {
	value, ok := row[i].(string)
	if !ok {
		return "", fmt.Errorf("read live access table %q column %q: %w", table, column, backup.ErrExportRead)
	}

	return value, nil
}

// overwriteStagedAccessTables replaces the access tables of the staged file
// with the live rows. Principals merge by ID: existing ones are updated,
// extra ones go after the children are gone, and missing ones are added
// last, so a deleted and recreated principal with the same subject does not
// hit the subject unique index. Every other access table is deleted and
// reloaded.
func overwriteStagedAccessTables(
	ctx context.Context, path string, specs []backup.PortableTable, live accessRows,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("keep access tables: %w", err)
	}

	conn, err := openStagedAccess(ctx, path)
	if err != nil {
		return err
	}

	defer func() {
		_ = conn.Close() //nolint:errcheck // Best-effort close of the staging handle.
	}()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("keep access tables: %w", backup.ErrExportRead)
	}

	committed := false

	defer func() {
		if !committed {
			_ = tx.Rollback() //nolint:errcheck // Best-effort rollback keeps the staged file.
		}
	}()

	for _, table := range keepClearTables() {
		clearQuery := `DELETE FROM "` + table + `"` // #nosec G202 // Table names are the fixed access tables.
		if _, err := tx.ExecContext(ctx, clearQuery); err != nil {
			return fmt.Errorf("clear staged access table %q: %w", table, backup.ErrExportRead)
		}
	}

	// Merge the principals before the child rows are reloaded: each reloaded
	// child row names a principal, so the principal row must exist first.
	if err := mergeStagedPrincipals(ctx, tx, live); err != nil {
		return err
	}

	for _, spec := range specs {
		if spec.Table == backup.TablePrincipals {
			continue
		}

		if err := insertStagedRows(ctx, tx, spec.Table, live.columns[spec.Table], live.rows[spec.Table]); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("keep access tables: %w", backup.ErrExportRead)
	}

	committed = true

	return nil
}

// keepClearTables lists the access tables that a keep clears from the
// staged file before it reloads them, children before parents so no delete
// breaks a foreign key. Principals merge by ID in mergeStagedPrincipals
// instead, so the list skips them. The order is the reverse of
// backup.AccessTables: the one list decides for both stores, so dropping a
// table from backup.AccessTables drops it from the keep on both dialects.
func keepClearTables() []string {
	out := make([]string, 0, len(backup.AccessTables))

	for _, table := range slices.Backward(backup.AccessTables) {
		if table == backup.TablePrincipals {
			continue
		}

		out = append(out, table)
	}

	return out
}

// mergeStagedPrincipals updates the staged principals the live access tables
// also hold, deletes the staged ones the live tables do not hold, and then
// inserts the live ones the staged file misses. The children are already
// gone, so every delete keeps the foreign keys. Update and delete run before
// the insert, so a principal deleted and created again with the same subject
// does not hit the subject unique index.
func mergeStagedPrincipals(ctx context.Context, tx *sql.Tx, live accessRows) error {
	columns := live.columns[backup.TablePrincipals]
	byID := columnIndex(columns, `"id"`)

	if byID < 0 {
		return fmt.Errorf("keep staged principals: %w", backup.ErrExportRead)
	}

	staged, err := stagedIDs(ctx, tx, backup.TablePrincipals)
	if err != nil {
		return err
	}

	keep := principalKeep{columns: columns, byID: byID, staged: staged}
	if err := updateExistingLivePrincipals(ctx, tx, live, keep); err != nil {
		return err
	}

	if err := deleteExtraPrincipals(ctx, tx, live, byID, staged); err != nil {
		return err
	}

	return insertMissingLivePrincipals(ctx, tx, live, keep)
}

// principalKeep holds the columns and IDs for a principal merge: the live
// columns, the ID column, and the staged IDs.
type principalKeep struct {
	columns []string
	byID    int
	staged  map[string]bool
}

// updateExistingLivePrincipals writes the live values over the staged
// principals with the same ID.
func updateExistingLivePrincipals(ctx context.Context, tx *sql.Tx, live accessRows, keep principalKeep) error {
	for _, row := range live.rows[backup.TablePrincipals] {
		id, ok := row[keep.byID].(string)
		if !ok {
			return fmt.Errorf("keep staged principals: %w", backup.ErrExportRead)
		}

		if !keep.staged[id] {
			continue
		}

		update := stagedRowUpdate{
			table:   backup.TablePrincipals,
			columns: keep.columns,
			idIndex: keep.byID,
			row:     row,
		}
		if err := updateStagedRow(ctx, tx, update); err != nil {
			return err
		}
	}

	return nil
}

// insertMissingLivePrincipals adds the live principals the staged file
// misses. It runs after the update and the delete, so a principal deleted
// and created again with the same subject does not hit the subject unique
// index.
func insertMissingLivePrincipals(ctx context.Context, tx *sql.Tx, live accessRows, keep principalKeep) error {
	for _, row := range live.rows[backup.TablePrincipals] {
		id, ok := row[keep.byID].(string)
		if !ok {
			return fmt.Errorf("keep staged principals: %w", backup.ErrExportRead)
		}

		if keep.staged[id] {
			continue
		}

		if err := insertStagedRows(ctx, tx, backup.TablePrincipals, keep.columns, [][]any{row}); err != nil {
			return err
		}
	}

	return nil
}

// deleteExtraPrincipals deletes the staged principals the live access tables
// do not hold.
func deleteExtraPrincipals(
	ctx context.Context, tx *sql.Tx, live accessRows, byID int, staged map[string]bool,
) error {
	liveIDs := livePrincipalIDs(live, byID)

	for id := range staged {
		if liveIDs[id] {
			continue
		}

		deleteQuery := `DELETE FROM "` + backup.TablePrincipals +
			`" WHERE "id" = ?` // #nosec G202 // Table name is the fixed principals table.
		if _, err := tx.ExecContext(ctx, deleteQuery, id); err != nil {
			return fmt.Errorf("clear extra staged principal: %w", backup.ErrExportRead)
		}
	}

	return nil
}

// livePrincipalIDs returns the IDs of the live principals.
func livePrincipalIDs(live accessRows, byID int) map[string]bool {
	out := map[string]bool{}

	for _, row := range live.rows[backup.TablePrincipals] {
		if id, ok := row[byID].(string); ok {
			out[id] = true
		}
	}

	return out
}

// stagedIDs returns the IDs of the staged table.
func stagedIDs(ctx context.Context, tx *sql.Tx, table string) (map[string]bool, error) {
	selectQuery := `SELECT "id" FROM "` + table + `"` // #nosec G202 // Table name is a fixed access table.
	rows, err := tx.QueryContext(ctx, selectQuery)
	if err != nil {
		return nil, fmt.Errorf("read staged access table %q: %w", table, backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Transactional read handle.
	}()

	out := map[string]bool{}

	for rows.Next() {
		var id string

		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, fmt.Errorf("read staged access table %q: %w", table, backup.ErrExportRead)
		}

		out[id] = true
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read staged access table %q: %w", table, backup.ErrExportRead)
	}

	return out, nil
}

// stagedRowUpdate holds one live row to write over the staged row with the
// same ID. The struct keeps updateStagedRow to five arguments or fewer.
type stagedRowUpdate struct {
	table   string
	columns []string
	idIndex int
	row     []any
}

// updateStagedRow writes one live row over the staged row with the same ID.
func updateStagedRow(ctx context.Context, tx *sql.Tx, update stagedRowUpdate) error {
	sets := make([]string, 0, len(update.columns)-1)
	args := make([]any, 0, len(update.columns))

	for i, name := range update.columns {
		if i == update.idIndex {
			continue
		}

		sets = append(sets, name+` = ?`)
		args = append(args, update.row[i])
	}

	args = append(args, update.row[update.idIndex])

	updateQuery := `UPDATE "` + update.table + `" SET ` + strings.Join(sets, ", ") +
		` WHERE "id" = ?` // #nosec G202 // Table name is a fixed access table.
	if _, err := tx.ExecContext(ctx, updateQuery, args...); err != nil {
		return fmt.Errorf("keep staged access table %q: %w", update.table, backup.ErrExportRead)
	}

	return nil
}

// insertStagedRows inserts rows into the staged access table with columns.
func insertStagedRows(
	ctx context.Context, tx *sql.Tx, table string, columns []string, rows [][]any,
) error {
	for start := 0; start < len(rows); start += portableBatchRows {
		end := min(start+portableBatchRows, len(rows))

		batch := rows[start:end]

		marks := strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")
		groups := make([]string, 0, len(batch))

		for range batch {
			groups = append(groups, "("+marks+")")
		}

		var args []any

		for _, row := range batch {
			args = append(args, row...)
		}

		insertQuery := `INSERT INTO "` + table + `" (` + strings.Join(columns, ", ") + `) VALUES ` +
			strings.Join(groups, ", ") // #nosec G202 // Table and column names come from the portable spec.
		if _, err := tx.ExecContext(ctx, insertQuery, args...); err != nil {
			return fmt.Errorf("keep staged access table %q: %w", table, backup.ErrExportRead)
		}
	}

	return nil
}

// columnIndex returns the index of name in columns, or -1.
func columnIndex(columns []string, name string) int {
	for i, column := range columns {
		if column == name {
			return i
		}
	}

	return -1
}

// openStagedAccess opens the staged file read-write with foreign keys on.
func openStagedAccess(ctx context.Context, path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("open staged access tables: %w", backup.ErrExportRead)
	}

	query := url.Values{}
	query.Set("_txlock", "immediate")
	query.Add("_pragma", "busy_timeout(10000)")
	query.Add("_pragma", "foreign_keys(on)")

	dsn := (&url.URL{Scheme: fileScheme, Path: abs, RawQuery: query.Encode()}).String()

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open staged access tables: %w", backup.ErrExportRead)
	}

	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close() //nolint:errcheck // Best-effort close after a failed open.

		return nil, fmt.Errorf("open staged access tables: %w", backup.ErrExportRead)
	}

	return conn, nil
}

// checkStagedForeignKeys runs PRAGMA foreign_key_check on the staged file
// after the access tables were kept: any row reports
// ErrRestoreAccessMismatch, so the apply changes nothing.
func checkStagedForeignKeys(ctx context.Context, path string) error {
	conn, err := openStagedAccess(ctx, path)
	if err != nil {
		return err
	}

	defer func() {
		_ = conn.Close() //nolint:errcheck // Best-effort close of the check handle.
	}()

	rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("check kept access tables: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Best-effort close of the check handle.
	}()

	tables := []string{}

	for rows.Next() {
		var (
			table  string
			rowid  int64
			parent string
			index  int64
		)

		if scanErr := rows.Scan(&table, &rowid, &parent, &index); scanErr != nil {
			return fmt.Errorf("check kept access tables: %w", backup.ErrExportRead)
		}

		if !slices.Contains(tables, table) {
			tables = append(tables, table)
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("check kept access tables: %w", backup.ErrExportRead)
	}

	if len(tables) > 0 {
		return fmt.Errorf("kept access tables break foreign keys of %q: %w", tables, backup.ErrRestoreAccessMismatch)
	}

	return nil
}
