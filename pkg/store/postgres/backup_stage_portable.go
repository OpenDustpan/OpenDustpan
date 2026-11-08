package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/backup"
)

// portableBatchRows is the number of rows of one multi-row INSERT and one
// transaction (ADR 42, decision 8). A batch fails as a whole.
const portableBatchRows = 200

// StagePortable implements backup.PortableStager. It builds the staging schema
// with the migrations of this schema version, loads the table files into it in
// foreign-key order, one transaction for each batch, and checks it as
// the checks of a restore require. The live tables are never written.
func (s *Store) StagePortable(
	ctx context.Context,
	open backup.PortableOpener,
	want backup.Summary,
	files []backup.PortableFile,
	opts backup.StageOptions,
) (backup.Summary, error) {
	in, err := s.portableStage(ctx, open, want, files, opts)
	if err != nil {
		return backup.Summary{}, err
	}

	staging, err := newStagingSchema()
	if err != nil {
		return backup.Summary{}, err
	}

	in.staging = staging

	return s.buildStaging(ctx, in)
}

// portableStage checks a stage of a portable backup before any row loads,
// and returns what buildChecked needs, with no staging schema name.
func (s *Store) portableStage(
	ctx context.Context,
	open backup.PortableOpener,
	want backup.Summary,
	files []backup.PortableFile,
	opts backup.StageOptions,
) (stagingInput, error) {
	var none stagingInput

	// Layer 1: compare the kind of the backup with the kind that this
	// database holds, before any row loads. The service checks too; this
	// check stays so that a caller of the port cannot skip it.
	live, kindErr := s.ReadKind(ctx)
	if kindErr != nil {
		return none, fmt.Errorf("stage portable backup: %w", kindErr)
	}

	if err := backup.CheckStageKind(opts, live); err != nil {
		return none, err
	}

	version, err := s.schemaVersion(ctx)
	if err != nil {
		return none, err
	}

	if schemaErr := backup.CheckStageSchema(want.SchemaVersion, version); schemaErr != nil {
		return none, fmt.Errorf("stage portable backup: %w", schemaErr)
	}

	index, err := backup.IndexPortableFiles(files, want.RowCounts)
	if err != nil {
		return none, err
	}

	return stagingInput{
		staging: "",
		load: func(ctx context.Context, schema string) error {
			return s.loadPortableStaging(ctx, schema, open, index)
		},
		want:      want,
		restoreAt: opts.RestoreAt,
	}, nil
}

// loadPortableStaging loads each table file into the staging schema, in the
// order of the portable spec. Each batch is one transaction. The control
// tables live in the control schema, which the staging area never loads, so
// only the seeded policy rows are cleared here.
func (s *Store) loadPortableStaging(
	ctx context.Context, schema string, open backup.PortableOpener, index map[string]backup.PortableFile,
) error {
	clearErr := s.inLongTx(ctx, backupBound(), readWriteTx(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+pgIdent(schema)); err != nil {
			return fmt.Errorf("set staging search path: %w", backup.ErrExportRead)
		}

		// The migration seeds the token policy row too (ADR 53).
		if _, err := tx.Exec(ctx, `DELETE FROM access_token_policy`); err != nil {
			return fmt.Errorf("clear staged token policy: %w", backup.ErrCorrupt)
		}

		// The migration seeds the session policy row too.
		if _, err := tx.Exec(ctx, `DELETE FROM session_policy`); err != nil {
			return fmt.Errorf("clear staged session policy: %w", backup.ErrCorrupt)
		}

		return nil
	})
	if clearErr != nil {
		return clearErr
	}

	for _, table := range backup.PortableTables() {
		if loadErr := s.loadPortableTable(ctx, schema, open, index[table.Table], table); loadErr != nil {
			return loadErr
		}
	}

	return nil
}

// readWriteTx returns the options of one load transaction: read committed, so
// each batch sees the rows of the batches before it.
func readWriteTx() pgx.TxOptions {
	//nolint:exhaustruct_v5 // The load needs only isolation and access mode; deferrable stays off.
	return pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadWrite}
}

// loadPortableTable reads one table file and inserts its rows in batches.
func (s *Store) loadPortableTable(
	ctx context.Context,
	schema string,
	open backup.PortableOpener,
	file backup.PortableFile,
	table backup.PortableTable,
) error {
	notNull, err := s.notNullColumns(ctx, schema, table.Table)
	if err != nil {
		return err
	}

	batch := make([][]any, 0, portableBatchRows)

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}

		if flushErr := s.insertPortableBatch(ctx, schema, table, batch); flushErr != nil {
			return flushErr
		}

		batch = batch[:0]

		return nil
	}

	err = backup.ReadPortableTable(ctx, open, file, table, func(values []any) error {
		batch = append(batch, pgValues(table, values, notNull))

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

// insertPortableBatch inserts the rows in one transaction with the search path of
// the staging schema.
func (s *Store) insertPortableBatch(
	ctx context.Context, schema string, table backup.PortableTable, rows [][]any,
) error {
	return s.inLongTx(ctx, backupBound(), readWriteTx(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+pgIdent(schema)); err != nil {
			return fmt.Errorf("set staging search path: %w", backup.ErrExportRead)
		}

		if _, err := tx.Exec(ctx, insertPortableSQL(table, len(rows)), flattenRows(rows)...); err != nil {
			return fmt.Errorf("load rows of table %q: %w", table.Table, backup.ErrCorrupt)
		}

		return nil
	})
}

// insertPortableSQL returns the multi-row INSERT of n rows of table. A JSON
// value is cast to jsonb.
func insertPortableSQL(table backup.PortableTable, n int) string {
	names := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		names = append(names, pgIdent(column.Name))
	}

	groups := make([]string, 0, n)
	next := 1

	for range n {
		marks := make([]string, 0, len(table.Columns))

		for _, column := range table.Columns {
			mark := fmt.Sprintf("$%d", next)
			if column.Kind == backup.KindJSON {
				mark += "::jsonb"
			}

			marks = append(marks, mark)
			next++
		}

		groups = append(groups, "("+strings.Join(marks, ", ")+")")
	}

	return "INSERT INTO " + pgIdent(
		table.Table,
	) + " (" + strings.Join(
		names,
		", ",
	) + ") VALUES " + // #nosec G202 // Constant names from the portable spec.
		strings.Join(
			groups,
			", ",
		)
}

// flattenRows returns the values of the rows in order, for one Exec.
func flattenRows(rows [][]any) []any {
	var out []any

	for _, row := range rows {
		out = append(out, row...)
	}

	return out
}

// pgValues returns one row with a missing time written as 0 for a NOT NULL
// column: the export wrote a SQLite 0 as null (ADR 42, decision 3).
func pgValues(table backup.PortableTable, values []any, notNull map[string]bool) []any {
	out := make([]any, len(values))

	for i, column := range table.Columns {
		out[i] = values[i]

		if values[i] == nil && column.Kind == backup.KindTime && notNull[column.Name] {
			out[i] = int64(0)
		}
	}

	return out
}

// notNullColumns returns the NOT NULL columns of table in the staging schema.
func (s *Store) notNullColumns(ctx context.Context, schema, table string) (map[string]bool, error) {
	rows, err := s.reads.Query(ctx,
		`SELECT column_name, is_nullable FROM information_schema.columns`+
			` WHERE table_schema = $1 AND table_name = $2`,
		schema, table,
	)
	if err != nil {
		return nil, fmt.Errorf("read columns of table %q: %w", table, backup.ErrSchemaRead)
	}

	defer rows.Close()

	out := make(map[string]bool)

	for rows.Next() {
		var name, nullable string

		if scanErr := rows.Scan(&name, &nullable); scanErr != nil {
			return nil, fmt.Errorf("scan columns of table %q: %w", table, backup.ErrSchemaRead)
		}

		out[name] = nullable == "NO"
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read columns of table %q: %w", table, backup.ErrSchemaRead)
	}

	return out, nil
}
