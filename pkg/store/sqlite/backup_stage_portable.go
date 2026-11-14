package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
)

// portableBatchRows is the number of rows that one INSERT and one transaction
// load (ADR 42, decision 8). A batch fails as a whole.
const portableBatchRows = 200

// StagePortable implements backup.PortableStager. It builds a staging file with
// the migrations of this schema version, loads the table files into it in
// foreign-key order, one transaction for each batch of rows, and checks it as
// the checks of a restore require. The live database is never written.
func (s *Store) StagePortable(
	ctx context.Context,
	open backup.PortableOpener,
	want backup.Summary,
	files []backup.PortableFile,
	opts backup.StageOptions,
) (backup.Summary, error) {
	index, err := s.portableStage(ctx, want, files, opts)
	if err != nil {
		return backup.Summary{}, err
	}

	temp, err := snapshotPath(filepath.Dir(s.path))
	if err != nil {
		return backup.Summary{}, err
	}

	sum, err := s.buildCheckedStaging(ctx, temp, open, portableCheck{
		index: index, want: want, restoreAt: opts.RestoreAt,
	})
	if err != nil {
		return backup.Summary{}, err
	}

	if err := s.keepStaged(temp); err != nil {
		removeSnapshot(temp)

		return backup.Summary{}, err
	}

	return sum, nil
}

// portableStage checks a stage of a portable backup before any row loads,
// and returns the index of its table files.
func (s *Store) portableStage(
	ctx context.Context, want backup.Summary, files []backup.PortableFile, opts backup.StageOptions,
) (map[string]backup.PortableFile, error) {
	// Layer 1: compare the kind of the backup with the kind that this
	// database holds, before any row loads. The service checks too; this
	// check stays so that a caller of the port cannot skip it.
	if kindErr := s.checkStageKind(ctx, opts); kindErr != nil {
		return nil, kindErr
	}

	version, err := func() (int64, error) {
		s.mu.RLock()
		defer s.mu.RUnlock()

		return schemaVersion(ctx, s.conn)
	}()
	if err != nil {
		return nil, err
	}

	if schemaErr := backup.CheckStageSchema(want.SchemaVersion, int(version)); schemaErr != nil {
		return nil, fmt.Errorf("stage portable backup: %w", schemaErr)
	}

	index, err := backup.IndexPortableFiles(files, want.RowCounts)
	if err != nil {
		return nil, err
	}

	return index, nil
}

// portableCheck holds what buildCheckedStaging loads, compares and checks
// after it builds the staging file.
type portableCheck struct {
	index     map[string]backup.PortableFile
	want      backup.Summary
	restoreAt time.Time
}

// buildCheckedStaging builds the staging file at path, loads the table files
// and checks the staged data against check.want. It returns the summary. A
// failure removes the file.
func (s *Store) buildCheckedStaging(
	ctx context.Context, path string, open backup.PortableOpener, check portableCheck,
) (backup.Summary, error) {
	if err := s.buildPortableStaging(ctx, path, open, check.index); err != nil {
		return backup.Summary{}, err
	}

	kept := false

	defer func() {
		if !kept {
			removeSnapshot(path)
		}
	}()

	sum, err := checkRestoreFile(ctx, path, check.want, check.restoreAt)
	if err != nil {
		return backup.Summary{}, err
	}

	if err := checkStagedBlobs(sum.Blobs, check.want.Blobs); err != nil {
		return backup.Summary{}, err
	}

	if err := backup.CheckUnverified(sum.Unverified, check.want.Unverified); err != nil {
		return backup.Summary{}, fmt.Errorf("staged backup: %w", err)
	}

	kept = true

	return sum, nil
}

// buildPortableStaging creates the staging file at path next to the live
// database, migrated to this schema, and loads the table files into it. A
// failed load removes it.
func (s *Store) buildPortableStaging(
	ctx context.Context, path string, open backup.PortableOpener, index map[string]backup.PortableFile,
) error {
	staged, err := Open(ctx, path)
	if err != nil {
		removeSnapshot(path)

		return fmt.Errorf("create staging file: %w", err)
	}

	loadErr := loadPortableTables(ctx, staged.conn, open, index)

	if closeErr := staged.Close(); closeErr != nil && loadErr == nil {
		loadErr = fmt.Errorf("close staging file: %w", closeErr)
	}

	if loadErr != nil {
		removeSnapshot(path)

		return loadErr
	}

	return nil
}

// loadPortableTables loads each table file into the staging database, in the
// order of the portable spec, which is the foreign-key order of Tables.
func loadPortableTables(
	ctx context.Context, conn *sql.DB, open backup.PortableOpener, index map[string]backup.PortableFile,
) error {
	// The migration seeds the installation row. The backup holds its own row, so
	// the seeded one goes first.
	if _, err := conn.ExecContext(ctx, `DELETE FROM installation`); err != nil {
		return fmt.Errorf("clear staged installation state: %w", backup.ErrExportRead)
	}

	// The migration seeds the token policy row too (ADR 53).
	if _, err := conn.ExecContext(ctx, `DELETE FROM access_token_policy`); err != nil {
		return fmt.Errorf("clear staged token policy: %w", backup.ErrExportRead)
	}

	// The migration seeds the session policy row too.
	if _, err := conn.ExecContext(ctx, `DELETE FROM session_policy`); err != nil {
		return fmt.Errorf("clear staged session policy: %w", backup.ErrExportRead)
	}

	for _, table := range backup.PortableTables() {
		if loadErr := loadPortableTable(ctx, conn, open, index[table.Table], table); loadErr != nil {
			return loadErr
		}
	}

	return nil
}

// loadPortableTable reads one table file and inserts its rows in batches, one
// transaction for each batch.
func loadPortableTable(
	ctx context.Context,
	conn *sql.DB,
	open backup.PortableOpener,
	file backup.PortableFile,
	table backup.PortableTable,
) error {
	notNull, err := notNullColumns(ctx, conn, table.Table)
	if err != nil {
		return err
	}

	batch := make([][]any, 0, portableBatchRows)

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}

		if flushErr := insertBatch(ctx, conn, table, batch); flushErr != nil {
			return flushErr
		}

		batch = batch[:0]

		return nil
	}

	err = backup.ReadPortableTable(ctx, open, file, table, func(values []any) error {
		batch = append(batch, sqliteValues(table, values, notNull))

		if len(batch) < portableBatchRows {
			return nil
		}

		return flush()
	})
	if err != nil {
		return err
	}

	return flush()
}

// insertBatch inserts the rows in one transaction, as one multi-row INSERT.
func insertBatch(ctx context.Context, conn *sql.DB, table backup.PortableTable, rows [][]any) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin load of table %q: %w", table.Table, backup.ErrExportRead)
	}

	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			_ = rollbackErr
		}
	}()

	if _, err := tx.ExecContext(ctx, insertSQL(table, len(rows)), flatten(rows)...); err != nil {
		return fmt.Errorf("load rows of table %q: %w", table.Table, backup.ErrCorrupt)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit load of table %q: %w", table.Table, backup.ErrExportRead)
	}

	return nil
}

// insertSQL returns the multi-row INSERT for n rows of table, with the SQLite
// names of the portable columns.
func insertSQL(table backup.PortableTable, n int) string {
	names := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		names = append(names, `"`+sqliteColumnName(table.Table, column.Name)+`"`)
	}

	marks := strings.TrimSuffix(strings.Repeat("?,", len(table.Columns)), ",")
	groups := make([]string, 0, n)

	for range n {
		groups = append(groups, "("+marks+")")
	}

	return `INSERT INTO "` + table.Table + `" (` + strings.Join(
		names,
		", ",
	) + `) VALUES ` + // #nosec G202 // Constant names from the portable spec.
		strings.Join(
			groups,
			", ",
		)
}

// flatten returns the values of the rows in order, for one Exec.
func flatten(rows [][]any) []any {
	var out []any

	for _, row := range rows {
		out = append(out, row...)
	}

	return out
}

// sqliteValues converts one row of portable values to SQLite storage: a boolean
// is 0 or 1, and a missing time is 0 for a NOT NULL column (the export wrote
// that 0 as null, ADR 42, decision 3).
func sqliteValues(table backup.PortableTable, values []any, notNull map[string]bool) []any {
	out := make([]any, len(values))

	for i, column := range table.Columns {
		out[i] = values[i]

		switch v := values[i].(type) {
		case bool:
			if v {
				out[i] = int64(1)
			} else {
				out[i] = int64(0)
			}
		case nil:
			if column.Kind == backup.KindTime && notNull[sqliteColumnName(table.Table, column.Name)] {
				out[i] = int64(0)
			}
		default:
			out[i] = values[i]
		}
	}

	return out
}

// notNullColumns returns the names of the NOT NULL columns of table in the
// staging database, by portable name.
func notNullColumns(ctx context.Context, conn *sql.DB, table string) (map[string]bool, error) {
	rows, err := conn.QueryContext(ctx, `SELECT name, "notnull" FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, fmt.Errorf("read columns of table %q: %w", table, backup.ErrSchemaRead)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	out := make(map[string]bool)

	for rows.Next() {
		var name string

		var notNullFlag int

		if err := rows.Scan(&name, &notNullFlag); err != nil {
			return nil, fmt.Errorf("scan columns of table %q: %w", table, backup.ErrSchemaRead)
		}

		out[name] = notNullFlag != 0
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read columns of table %q: %w", table, backup.ErrSchemaRead)
	}

	return out, nil
}
