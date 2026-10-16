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
// staged file passed every check in StagePortable. A backup that names a
// session of a principal the current access tables do not hold reports
// ErrRestoreAccessMismatch and changes nothing.
func (s *Store) ApplyStagingKeepingAccess(ctx context.Context) error {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()

	if s.stagingPath == "" {
		return backup.ErrCorrupt
	}

	staged := s.stagingPath

	if err := s.keepLiveAccessTables(ctx, staged); err != nil {
		return err
	}

	if err := fenceStagedFile(ctx, staged); err != nil {
		return err
	}

	if err := s.swapDatabase(ctx, staged); err != nil {
		return err
	}

	s.stagingPath = ""

	return nil
}

// keepLiveAccessTables replaces the access tables of the staged file at path
// with the current live rows. Sessions stay as the backup holds them: a
// session of a principal the live access tables do not hold reports
// ErrRestoreAccessMismatch before any write to the staged file.
func (s *Store) keepLiveAccessTables(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("keep access tables: %w", err)
	}

	specs := accessTableSpecs()

	live, err := s.readLiveAccessTables(ctx, specs)
	if err != nil {
		return err
	}

	if err := checkStagedSessions(ctx, path, live.principalIDs()); err != nil {
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

// principalIDs returns the IDs of the live principals.
func (a accessRows) principalIDs() map[string]bool {
	out := map[string]bool{}

	for _, row := range a.rows["principals"] {
		columns := a.columns["principals"]

		for i, name := range columns {
			if name == `"id"` {
				if id, ok := row[i].(string); ok {
					out[id] = true
				}
			}
		}
	}

	return out
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
// caller holds no lock: one shared hold covers all reads, so the rows come
// from one consistent view.
func (s *Store) readLiveAccessTables(
	ctx context.Context, specs []backup.PortableTable,
) (accessRows, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

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

// checkStagedSessions reports ErrRestoreAccessMismatch when the staged backup
// holds a session of a principal the live access tables do not hold.
func checkStagedSessions(ctx context.Context, path string, livePrincipals map[string]bool) error {
	conn, err := openStagedAccess(ctx, path)
	if err != nil {
		return err
	}

	defer func() {
		_ = conn.Close() //nolint:errcheck // Read-only check handle.
	}()

	rows, err := conn.QueryContext(ctx, `SELECT DISTINCT principal_id FROM sessions`)
	if err != nil {
		return fmt.Errorf("check staged sessions: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	for rows.Next() {
		var principalID string

		if scanErr := rows.Scan(&principalID); scanErr != nil {
			return fmt.Errorf("check staged sessions: %w", backup.ErrExportRead)
		}

		if !livePrincipals[principalID] {
			return fmt.Errorf(
				"staged session names unknown principal %q: %w",
				principalID,
				backup.ErrRestoreAccessMismatch,
			)
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("check staged sessions: %w", backup.ErrExportRead)
	}

	return nil
}

// overwriteStagedAccessTables replaces the access tables of the staged file
// with the live rows. Principals merge by ID so sessions of live principals
// keep their parent: missing principals are added, existing ones are
// updated, and extra ones go after the children are gone. Every other access
// table is deleted and reloaded.
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

	for _, table := range []string{
		backup.TableGroupMembers,
		backup.TableAccessTokens,
		backup.TableGrants,
		backup.TableRoles,
		backup.TablePrincipalGroups,
	} {
		clearQuery := `DELETE FROM "` + table + `"` // #nosec G202 // Table names are the fixed access tables.
		if _, err := tx.ExecContext(ctx, clearQuery); err != nil {
			return fmt.Errorf("clear staged access table %q: %w", table, backup.ErrExportRead)
		}
	}

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

// mergeStagedPrincipals adds the live principals the staged file misses,
// updates the ones both hold, and deletes the extra ones. The children are
// already gone and the sessions name only live principals, so every delete
// keeps the foreign keys.
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
	if err := upsertLivePrincipals(ctx, tx, live, keep); err != nil {
		return err
	}

	return deleteExtraPrincipals(ctx, tx, live, byID, staged)
}

// principalKeep holds the columns and IDs for a principal merge: the live
// columns, the ID column, and the staged IDs.
type principalKeep struct {
	columns []string
	byID    int
	staged  map[string]bool
}

// upsertLivePrincipals adds the live principals the staged file misses and
// updates the ones both hold.
func upsertLivePrincipals(ctx context.Context, tx *sql.Tx, live accessRows, keep principalKeep) error {
	for _, row := range live.rows[backup.TablePrincipals] {
		id, ok := row[keep.byID].(string)
		if !ok {
			return fmt.Errorf("keep staged principals: %w", backup.ErrExportRead)
		}

		if !keep.staged[id] {
			if err := insertStagedRows(ctx, tx, backup.TablePrincipals, keep.columns, [][]any{row}); err != nil {
				return err
			}

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
