package sqlite

import (
	"context"
	"crypto/md5" //nolint:gosec // Size only: state versions carry an MD5 by protocol, not for security.
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/internal/blobrefs"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var (
	_ backup.Registry  = (*Store)(nil)
	_ backup.Inspector = (*Store)(nil)
	_ backup.KeyPeeker = (*Store)(nil)
)

// CreateBackup implements backup.Registry: it stores r as a running backup.
// The row carries no fence: it describes the installation, not one namespace.
func (s *Store) CreateBackup(ctx context.Context, r backup.Record) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := codec.ToBackupRow(r)

	n, err := s.queries.CreateBackup(ctx, db.CreateBackupParams{
		ID:          row.ID,
		State:       row.State,
		StartedAtMs: row.StartedAtMs,
		EndedAtMs:   optionalMillis(r.EndedAt),
		Target:      row.Target,
		Error:       row.Error,
	})
	if err != nil {
		return fmt.Errorf("insert backup: %w", err)
	}

	if n == 0 {
		return s.createBackupConflict(ctx, r.ID)
	}

	return nil
}

// GetBackup implements backup.Registry.
func (s *Store) GetBackup(ctx context.Context, id backup.ID) (backup.Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.fetchBackup(ctx, id)
}

// fetchBackup reads one backup row. The caller holds the store lock.
func (s *Store) fetchBackup(ctx context.Context, id backup.ID) (backup.Record, error) {
	row, err := s.queries.GetBackup(ctx, id.String())
	if errors.Is(err, sql.ErrNoRows) {
		return backup.Record{}, backup.ErrNotFound
	}

	if err != nil {
		return backup.Record{}, fmt.Errorf("select backup: %w", err)
	}

	return backupFromRow(row)
}

// ListBackups implements backup.Registry: every backup row, newest first.
func (s *Store) ListBackups(ctx context.Context) ([]backup.Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.ListBackups(ctx)
	if err != nil {
		return nil, fmt.Errorf("select backups: %w", err)
	}

	out := make([]backup.Record, 0, len(rows))

	for _, row := range rows {
		r, err := backupFromRow(row)
		if err != nil {
			return nil, err
		}

		out = append(out, r)
	}

	return out, nil
}

// StartBackup implements backup.Registry: it stores r as a running backup and
// starts the operation start in one transaction. It reports ErrAlreadyExists
// when the ID is taken, and ErrAlreadyRunning when the unique index on
// running backups refuses the row. A failed start changes nothing.
func (s *Store) StartBackup(ctx context.Context, r backup.Record, start durable.StartRequest) error {
	if err := checkBackupStart(r, start); err != nil {
		return err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inMinimumTx(ctx, func(q *db.Queries) error {
		_, err := startBackupTx(ctx, q, r, start)

		return err
	})
}

// StartBackupClaimed implements backup.Registry: it starts backup r as
// StartBackup does, and claims the new operation for claim in the same
// transaction. It returns the claimed operation.
func (s *Store) StartBackupClaimed(
	ctx context.Context, r backup.Record, start durable.StartRequest, claim durable.StartClaim,
) (durable.Operation, error) {
	if err := claim.Validate(); err != nil {
		return durable.Operation{}, err
	}

	if err := checkBackupStart(r, start); err != nil {
		return durable.Operation{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var op durable.Operation

	err := s.inMinimumTx(ctx, func(q *db.Queries) error {
		started, err := startBackupTx(ctx, q, r, start)
		if err != nil {
			return err
		}

		op, err = claimStarted(ctx, q, started.ID, claim)

		return err
	})
	if err != nil {
		return durable.Operation{}, err
	}

	return op, nil
}

// checkBackupStart checks the operation start of backup r before a start.
func checkBackupStart(r backup.Record, start durable.StartRequest) error {
	if err := start.Validate(); err != nil {
		return err
	}

	// Defense in depth: the service builds the request from the backup ID.
	// This check stays so that no caller can start the row of one backup
	// with the operation of another kind or key.
	if start.Key != backup.OperationKey(r.ID) || start.Kind != backup.OperationKind {
		return fmt.Errorf("start backup %s: operation %q of kind %q: %w",
			r.ID, start.Key, start.Kind, durable.ErrKeyConflict)
	}

	return nil
}

// startBackupTx stores r as a running backup and starts the operation start,
// in the transaction of q, and returns the new operation.
func startBackupTx(
	ctx context.Context, q *db.Queries, r backup.Record, start durable.StartRequest,
) (durable.Operation, error) {
	row := codec.ToBackupRow(r)

	n, err := q.CreateBackup(ctx, db.CreateBackupParams{
		ID:          row.ID,
		State:       string(backup.StateRunning),
		StartedAtMs: row.StartedAtMs,
		EndedAtMs:   optionalMillis(r.EndedAt),
		Target:      row.Target,
		Error:       "",
	})
	if err != nil {
		return durable.Operation{}, fmt.Errorf("insert backup: %w", err)
	}

	if n == 0 {
		return durable.Operation{}, backupConflict(ctx, q, r.ID)
	}

	op, created, err := startDurable(ctx, q, start)
	if err != nil {
		return durable.Operation{}, err
	}

	if !created {
		// A new row needs a new operation: a taken key is a start of
		// another backup.
		return durable.Operation{}, fmt.Errorf("start backup operation %q: %w", start.Key, durable.ErrKeyConflict)
	}

	return op, nil
}

// FinishBackup implements backup.Registry: it checks the fence f, records
// its step and moves the running backup id to state with the error text and
// the end time, in one transaction. A fence that is not the live claim
// reports the durable sentinel and changes nothing.
//
//nolint:revive // argument-limit: the fence, the row and the three fields of its end are one write; a struct would only move the fields.
func (s *Store) FinishBackup(
	ctx context.Context, f durable.StepFence, id backup.ID, state backup.State, errText string, endedAt time.Time,
) (backup.Record, error) {
	if state != backup.StateSucceeded && state != backup.StateFailed {
		return backup.Record{}, backup.ErrNotRunning
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var out backup.Record

	err := s.inStepTx(ctx, func(ctx context.Context, _ *sql.Tx, q *db.Queries) error {
		// The fence comes first: a stale claim changes nothing and learns
		// nothing about the row.
		if err := recordStep(ctx, q, f, nil); err != nil {
			return err
		}

		// Defense in depth: the fence checked above is live, but it must
		// belong to the operation of this backup. The service passes the
		// right fence; this check stays so that no caller can finish a row
		// under the claim of another operation.
		if err := ownsBackup(ctx, q, f, id); err != nil {
			return err
		}

		rec, err := finishRow(ctx, q, id, state, errText, endedAt)
		out = rec

		return err
	})
	if err != nil {
		return backup.Record{}, err
	}

	return out, nil
}

// ownsBackup returns durable.ErrClaimLost unless the operation of f is the
// operation that runs backup id.
func ownsBackup(ctx context.Context, q *db.Queries, f durable.StepFence, id backup.ID) error {
	op, err := q.GetDurableOperation(ctx, string(f.Operation))
	if err != nil {
		return fmt.Errorf("select operation of the fence: %w", err)
	}

	if op.Kind != string(backup.OperationKind) || op.StartKey != backup.OperationKey(id) {
		return durable.ErrClaimLost
	}

	return nil
}

// finishRow moves the running row id to state in the transaction of q, and
// returns the finished row. It reports ErrNotFound for a missing row and
// ErrNotRunning for a row that is not running.
//
//nolint:revive // argument-limit: the transaction, the row and the three fields of its end are one write; a struct would only move the fields.
func finishRow(
	ctx context.Context, q *db.Queries, id backup.ID, state backup.State, errText string, endedAt time.Time,
) (backup.Record, error) {
	if _, err := q.GetBackup(ctx, id.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return backup.Record{}, backup.ErrNotFound
		}

		return backup.Record{}, fmt.Errorf("select backup: %w", err)
	}

	n, err := q.FinishBackup(ctx, db.FinishBackupParams{
		State:     string(state),
		Error:     errText,
		EndedAtMs: optionalMillis(endedAt),
		ID:        id.String(),
	})
	if err != nil {
		return backup.Record{}, fmt.Errorf("finish backup: %w", err)
	}

	if n == 0 {
		return backup.Record{}, backup.ErrNotRunning
	}

	row, err := q.GetBackup(ctx, id.String())
	if err != nil {
		return backup.Record{}, fmt.Errorf("select finished backup: %w", err)
	}

	return backupFromRow(row)
}

// FailEndedBackups implements backup.Registry: it marks failed each running
// row whose operation ended failed or canceled. The error text is the last
// error of the operation.
func (s *Store) FailEndedBackups(ctx context.Context, endedAt time.Time) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n, err := s.queries.FailEndedBackups(ctx, db.FailEndedBackupsParams{
		Prefix:    backup.OperationKeyPrefix,
		Fallback:  backup.EndedWithoutFinishText,
		EndedAtMs: optionalMillis(endedAt),
		Kind:      string(backup.OperationKind),
	})
	if err != nil {
		return 0, fmt.Errorf("fail ended backups: %w", err)
	}

	return int(n), nil
}

// backupFromRow converts a backup row into a domain record. Unknown states
// report a corrupt row: every writer validates the state first.
func backupFromRow(row db.Backup) (backup.Record, error) {
	var endedAt time.Time

	if row.EndedAtMs.Valid {
		endedAt = codec.FromMillis(row.EndedAtMs.Int64)
	}

	return codec.FromBackupRow(codec.BackupRow{
		ID:          row.ID,
		State:       row.State,
		Target:      row.Target,
		Error:       row.Error,
		StartedAtMs: row.StartedAtMs,
		EndedAtMs:   codec.OptionalMillis(endedAt),
	})
}

// optionalMillis encodes t as a nullable Unix millisecond column. The zero
// time is NULL: the row has no end time.
func optionalMillis(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{Int64: 0, Valid: false}
	}

	return sql.NullInt64{Int64: codec.Millis(t), Valid: true}
}

// CheckCoverage implements backup.Coverage: it compares the column list of
// each table of backup.Tables and of backup.ControlTables with the columns
// that the schema holds for the table. A table without a list, a column that
// differs, or a live table in neither list fails with backup.ErrCoverage.
func (s *Store) CheckCoverage(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	lists := make(map[string][]string, len(backup.Tables)+len(backup.ControlTables)+len(backup.EmptiedTables))

	for _, spec := range sqliteBackupTables() {
		lists[spec.table] = spec.columns
	}

	for _, spec := range sqliteControlTables() {
		lists[spec.table] = spec.columns
	}

	for _, spec := range sqliteEmptiedTables() {
		lists[spec.table] = spec.columns
	}

	if err := s.checkListedTables(ctx, backup.Tables, lists, "backup"); err != nil {
		return err
	}

	if err := s.checkListedTables(ctx, backup.ControlTables, lists, "control"); err != nil {
		return err
	}

	if err := s.checkListedTables(ctx, backup.EmptiedTables, lists, "emptied"); err != nil {
		return err
	}

	if err := s.checkNoUnknownTables(ctx, lists); err != nil {
		return err
	}

	return s.checkPortableColumns(ctx)
}

// checkListedTables checks the column lists of tables in one coverage list.
// The caller holds the store read lock.
func (s *Store) checkListedTables(
	ctx context.Context, tables []string, lists map[string][]string, kind string,
) error {
	for _, table := range tables {
		listed, ok := lists[table]
		if !ok {
			return fmt.Errorf("%s table %q has no column list: %w", kind, table, backup.ErrCoverage)
		}

		live, err := s.liveColumns(ctx, table)
		if err != nil {
			return err
		}

		if checkErr := backup.CheckColumns(table, listed, live); checkErr != nil {
			return checkErr
		}
	}

	return nil
}

// checkNoUnknownTables fails with ErrCoverage when the schema holds a data
// table in neither the backup nor the control list. The caller holds the
// store read lock.
func (s *Store) checkNoUnknownTables(ctx context.Context, lists map[string][]string) error {
	const query = `SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name NOT IN ('schema_migrations', 'audit_chain_state')
		ORDER BY name`

	rows, err := s.conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("select schema tables: %w", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	for rows.Next() {
		var table string

		if scanErr := rows.Scan(&table); scanErr != nil {
			return fmt.Errorf("scan schema table: %w", scanErr)
		}

		if _, ok := lists[table]; !ok {
			return fmt.Errorf("schema holds table %q in neither the backup nor the control list: %w",
				table, backup.ErrCoverage)
		}
	}

	if iterErr := rows.Err(); iterErr != nil {
		return fmt.Errorf("read schema tables: %w", iterErr)
	}

	return nil
}

// checkPortableColumns compares the portable spec with the live columns of each
// table, under the SQLite names of the portable columns. The export writes the
// spec, so the spec must match the schema (ADR 42, decisions 3 and 5).
func (s *Store) checkPortableColumns(ctx context.Context) error {
	for _, spec := range backup.PortableTables() {
		listed := make([]string, 0, len(spec.Columns))
		for _, column := range spec.Columns {
			listed = append(listed, sqliteColumnName(spec.Table, column.Name))
		}

		live, err := s.liveColumns(ctx, spec.Table)
		if err != nil {
			return err
		}

		if checkErr := backup.CheckColumns(spec.Table, listed, live); checkErr != nil {
			return checkErr
		}
	}

	return nil
}

// liveColumns returns the column names of table in schema order, read with
// pragma_table_info.
func (s *Store) liveColumns(ctx context.Context, table string) ([]string, error) {
	rows, err := s.conn.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		return nil, fmt.Errorf("read columns of table %q: %w", table, backup.ErrSchemaRead)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	var columns []string

	for rows.Next() {
		var name string

		if scanErr := rows.Scan(&name); scanErr != nil {
			return nil, fmt.Errorf("scan column of table %q: %w", table, backup.ErrSchemaRead)
		}

		columns = append(columns, name)
	}

	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("read columns of table %q: %w", table, backup.ErrSchemaRead)
	}

	return columns, nil
}

// SchemaTables implements backup.Inspector: the data tables the schema holds,
// read from the catalog, never from backup.Tables, so the conformance suite
// fails while the two differ. It excludes bookkeeping (schema_migrations
// and audit_chain_state, the chain state of this installation)
// and SQLite internals.
func (s *Store) SchemaTables(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	const query = `SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name NOT IN ('schema_migrations', 'audit_chain_state')
		ORDER BY name`

	rows, err := s.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("select schema tables: %w", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	out := []string{}

	for rows.Next() {
		var name string

		if scanErr := rows.Scan(&name); scanErr != nil {
			return nil, fmt.Errorf("scan schema table: %w", scanErr)
		}

		out = append(out, name)
	}

	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("read schema tables: %w", iterErr)
	}

	return out, nil
}

// keepStaged makes temp the staging area, discarding the previous one. A failed
// discard returns the error and keeps the new file out of the staging area.
func (s *Store) keepStaged(temp string) error {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()

	if s.stagingPath != "" {
		if err := removeSnapshotErr(s.stagingPath); err != nil {
			return fmt.Errorf("discard previous staging area: %w", err)
		}
	}

	s.stagingPath = temp

	return nil
}

// DiscardStaging implements backup.CheckStore: it removes the staged restore file,
// if any. It leaves the live database unchanged. A failed remove returns the
// error and retains the staging area for retry, matching the PostgreSQL
// adapter.
func (s *Store) DiscardStaging(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("discard staging area: %w", err)
	}

	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()

	if s.stagingPath == "" {
		return nil
	}

	if err := removeSnapshotErr(s.stagingPath); err != nil {
		return fmt.Errorf("discard staging area: %w", err)
	}

	s.stagingPath = ""

	return nil
}

// PeekPortableKey implements backup.KeyPeeker: it reads one data key of
// wrapperID from the secret_keys file of a portable backup (ADR 42).
func (s *Store) PeekPortableKey(
	ctx context.Context, open backup.PortableOpener, file backup.PortableFile, wrapperID string,
) (secret.WrappedKey, error) {
	return codec.PeekPortableSecretKey(ctx, open, file, wrapperID)
}

// PeekPortableInstallationKey implements backup.InstallationKeyPeeker: it
// reads one installation key of wrapperID from the installation_secret_keys
// file of a portable backup.
func (s *Store) PeekPortableInstallationKey(
	ctx context.Context, open backup.PortableOpener, file backup.PortableFile, wrapperID string,
) (secret.WrappedKey, error) {
	return codec.PeekPortableInstallationKey(ctx, open, file, wrapperID)
}

// capture copies the database into a new file with VACUUM INTO and returns
// its path. The target must not exist, so it reserves a new name first. The
// caller removes the file.
func (s *Store) capture(ctx context.Context) (string, error) {
	snap, err := snapshotPath(filepath.Dir(s.path))
	if err != nil {
		return "", err
	}

	vacuum := "VACUUM INTO " + quoteLiteral(snap) //nolint:gosec // No bind parameter; quoted temp path.

	if _, err := s.conn.ExecContext(ctx, vacuum); err != nil {
		removeSnapshot(snap)

		return "", fmt.Errorf("capture backup snapshot: %w", err)
	}

	return snap, nil
}

// swapDatabaseLocked replaces the destination file with the staged file with
// the store lock already held: the
// keep-access apply holds the write lock from the live snapshot through the
// swap, so it swaps without taking the lock again. The fence transaction
// already copied the control tables into the staged file, so the rename
// keeps the live control rows with no further copy.
func (s *Store) swapDatabaseLocked(ctx context.Context, temp string) error {
	if err := s.conn.Close(); err != nil {
		return fmt.Errorf("close database for restore: %w", err)
	}

	for _, name := range []string{s.path + "-wal", s.path + "-shm"} {
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			reopenErr := s.reopenDatabase(ctx)
			return errors.Join(fmt.Errorf("remove write-ahead log: %w", err), reopenErr)
		}
	}

	if err := os.Rename(temp, s.path); err != nil {
		renameErr := fmt.Errorf("move restored database into place: %w", err)
		reopenErr := s.reopenDatabase(ctx)

		return errors.Join(renameErr, reopenErr)
	}

	conn, err := openDatabase(ctx, s.path, nil)
	if err != nil {
		return err
	}

	s.conn = conn
	s.queries = db.New(conn)

	return nil
}

// reopenDatabase reopens the store on its current path after a swap failure
// before the rename. Restore runs offline, so the destination stays usable.
// The caller holds the store lock.
func (s *Store) reopenDatabase(ctx context.Context) error {
	conn, err := openDatabase(ctx, s.path, nil)
	if err != nil {
		return err
	}

	s.conn = conn
	s.queries = db.New(conn)

	return nil
}

// checkRestoreFile opens the staged file, checks its integrity, reads its
// summary, and compares the schema version and the row counts with the
// manifest. It returns the summary of what the swap will load.
func checkRestoreFile(
	ctx context.Context, path string, want backup.Summary, restoreAt time.Time,
) (backup.Summary, error) {
	conn, err := openSnapshot(ctx, path)
	if err != nil {
		return backup.Summary{}, fmt.Errorf("check staged backup: %w", corrupt(err))
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	if integrityErr := checkIntegrity(ctx, conn); integrityErr != nil {
		return backup.Summary{}, fmt.Errorf("check staged backup: %w", corrupt(integrityErr))
	}

	if keysErr := checkForeignKeys(ctx, conn); keysErr != nil {
		return backup.Summary{}, fmt.Errorf("check staged backup: %w", corrupt(keysErr))
	}

	if sealedErr := checkSealedStateVersions(ctx, conn); sealedErr != nil {
		return backup.Summary{}, fmt.Errorf("check staged backup: %w", sealedErr)
	}

	if rowsErr := checkStagedRows(ctx, conn, restoreAt); rowsErr != nil {
		return backup.Summary{}, fmt.Errorf("check staged backup: %w", rowsErr)
	}

	sum, err := summarizeConn(ctx, conn)
	if err != nil {
		return backup.Summary{}, fmt.Errorf("check staged backup: %w", corrupt(err))
	}

	if sum.SchemaVersion != want.SchemaVersion {
		return backup.Summary{}, fmt.Errorf(
			"staged backup schema version %d differs from manifest %d: %w",
			sum.SchemaVersion,
			want.SchemaVersion,
			backup.ErrCorrupt,
		)
	}

	if err := checkRowCounts(sum.RowCounts, want.RowCounts); err != nil {
		return backup.Summary{}, err
	}

	return sum, nil
}

// checkRowCounts compares loaded row counts with the manifest. A count that
// differs, a table the backup list does not know, or a table the manifest
// omits reports corruption: an omitted empty table cannot slip through the
// count check, matching the PostgreSQL adapter.
func checkRowCounts(loaded, want map[string]int64) error {
	for _, table := range backup.Tables {
		if _, ok := want[table]; !ok {
			return fmt.Errorf("import manifest omits table %q: %w", table, backup.ErrCorrupt)
		}

		if loaded[table] != want[table] {
			return fmt.Errorf(
				"import row counts %v differ from manifest %v: %w",
				loaded,
				want,
				backup.ErrCorrupt,
			)
		}
	}

	for table := range want {
		if !slices.Contains(backup.Tables, table) {
			return fmt.Errorf("import manifest names unknown table %q: %w", table, backup.ErrCorrupt)
		}
	}

	return nil
}

// checkForeignKeys runs PRAGMA foreign_key_check on the staged database: any
// row reports a child row without its parent. A valid backup never reports
// one: the live database enforces its foreign keys on every write.
func checkForeignKeys(ctx context.Context, conn *sql.DB) error {
	rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("check backup foreign keys: %w", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
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
			return fmt.Errorf("read backup foreign keys: %w", scanErr)
		}

		if !slices.Contains(tables, table) {
			tables = append(tables, table)
		}
	}

	if iterErr := rows.Err(); iterErr != nil {
		return fmt.Errorf("read backup foreign keys: %w", iterErr)
	}

	if len(tables) > 0 {
		return fmt.Errorf("backup breaks foreign keys of %q: %w", tables, backup.ErrCorrupt)
	}

	return nil
}

// checkStagedRows checks the rows of the staged database with the rules of
// the store: the namespace IDs, the owned rows, the grants and the
// memberships of principals of a namespace, and the feature rows.
func checkStagedRows(ctx context.Context, conn *sql.DB, restoreAt time.Time) error {
	if idsErr := checkStagedNamespaceIDs(ctx, conn); idsErr != nil {
		return idsErr
	}

	if ownedErr := checkStagedOwnedRows(ctx, conn, restoreAt); ownedErr != nil {
		return ownedErr
	}

	if grantErr := checkStagedNamespacePrincipalGrants(ctx, conn); grantErr != nil {
		return grantErr
	}

	if memberErr := checkStagedNamespacePrincipalMemberships(ctx, conn); memberErr != nil {
		return memberErr
	}

	return checkStagedFeatureRows(ctx, conn)
}

// checkStagedNamespaceIDs refuses a staged backup with a namespace whose ID is
// not a valid namespace ID. It returns ErrCorrupt.
func checkStagedNamespaceIDs(ctx context.Context, conn *sql.DB) error {
	rows, err := conn.QueryContext(ctx, `SELECT name, id FROM namespaces`)
	if err != nil {
		return fmt.Errorf("read staged namespaces: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	for rows.Next() {
		var name, id string

		if scanErr := rows.Scan(&name, &id); scanErr != nil {
			return fmt.Errorf("read staged namespaces: %w", backup.ErrExportRead)
		}

		if _, parseErr := namespace.ParseID(id); parseErr != nil {
			return fmt.Errorf("staged namespace %q has an invalid ID: %w: %w", name, parseErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged namespaces: %w", backup.ErrExportRead)
	}

	return nil
}

// checkSealedStateVersions refuses a staged backup with a state version that
// is not sealed: every stored version is sealed, so an import with a plain
// row fails instead of serving plain state.
func checkSealedStateVersions(ctx context.Context, conn *sql.DB) error {
	const query = `SELECT EXISTS(SELECT 1 FROM state_versions WHERE sealed = 0)`

	var unsealed bool

	if err := conn.QueryRowContext(ctx, query).Scan(&unsealed); err != nil {
		return fmt.Errorf("read staged state versions: %w", backup.ErrExportRead)
	}

	if unsealed {
		return fmt.Errorf("import holds an unsealed state version: %w", workspace.ErrUnsealedState)
	}

	return nil
}

// checkStagedBlobs compares the blob references of the staged data with the
// manifest: the staged data must name exactly the keys of the manifest with
// the same sealed flags. Unsealed entries also match in size and checksum;
// sealed entries name sealed stored bytes in the manifest, so only the key
// and the flag match.
func checkStagedBlobs(staged, want []backup.BlobRef) error {
	byKey := func(a, b backup.BlobRef) int { return strings.Compare(a.Key, b.Key) }

	ordered := slices.Clone(staged)
	slices.SortFunc(ordered, byKey)

	wanted := slices.Clone(want)
	slices.SortFunc(wanted, byKey)

	same := func(a, b backup.BlobRef) bool {
		if a.Key != b.Key || a.Sealed != b.Sealed {
			return false
		}

		return a.Sealed || (a.Size == b.Size && a.MD5 == b.MD5)
	}

	if !slices.EqualFunc(ordered, wanted, same) {
		return fmt.Errorf(
			"staged backup names %d blob references, the manifest names %d: %w",
			len(ordered),
			len(wanted),
			backup.ErrCorrupt,
		)
	}

	return nil
}

// checkIntegrity runs PRAGMA integrity_check: exactly one row holding "ok".
func checkIntegrity(ctx context.Context, conn *sql.DB) error {
	rows, err := conn.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return fmt.Errorf("check backup integrity: %w", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	messages := []string{}

	for rows.Next() {
		var message string

		if scanErr := rows.Scan(&message); scanErr != nil {
			return fmt.Errorf("read backup integrity: %w", scanErr)
		}

		messages = append(messages, message)
	}

	if iterErr := rows.Err(); iterErr != nil {
		return fmt.Errorf("read backup integrity: %w", iterErr)
	}

	if len(messages) != 1 || messages[0] != "ok" {
		return fmt.Errorf("backup integrity check reports %q: %w", messages, backup.ErrCorrupt)
	}

	return nil
}

// summarizeFile opens the snapshot file at path and returns its summary.
func summarizeFile(ctx context.Context, path string) (backup.Summary, error) {
	conn, err := openSnapshot(ctx, path)
	if err != nil {
		return backup.Summary{}, err
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	return summarizeConn(ctx, conn)
}

// openSnapshot opens a snapshot file read-only: the VACUUM INTO copy or the
// staged restore file, never the live database.
func openSnapshot(ctx context.Context, path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve snapshot path: %w", err)
	}

	query := url.Values{}
	query.Set(dsnModeKey, "ro")
	query.Add("_pragma", "query_only(on)")
	query.Add("_pragma", "busy_timeout(10000)")

	dsn := (&url.URL{Scheme: fileScheme, Path: abs, RawQuery: query.Encode()}).String()

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open backup snapshot: %w", err)
	}

	if pingErr := conn.PingContext(ctx); pingErr != nil {
		return nil, errors.Join(fmt.Errorf("open backup snapshot: %w", pingErr), conn.Close())
	}

	return conn, nil
}

// summarizeConn reads the backup summary of an open database: the dialect,
// the schema version, the primary installations, the wrapper IDs, the row count of
// each table in backup.Tables, and the blob references. Callers pass a
// snapshot, never the live database, so the counts and the references
// describe one consistent view.
func summarizeConn(ctx context.Context, conn *sql.DB) (backup.Summary, error) {
	version, err := schemaVersion(ctx, conn)
	if err != nil {
		return backup.Summary{}, err
	}

	installations, err := stringColumn(
		ctx,
		conn,
		`SELECT DISTINCT primary_installation FROM namespaces ORDER BY primary_installation`,
	)
	if err != nil {
		return backup.Summary{}, err
	}

	wrappers, err := stringColumn(
		ctx,
		conn,
		`SELECT wrapper_id FROM secret_keys UNION SELECT wrapper_id FROM installation_secret_keys
		UNION SELECT wrapper_id FROM item_keys WHERE shredded_at_ms = 0 ORDER BY wrapper_id`,
	)
	if err != nil {
		return backup.Summary{}, err
	}

	counts := make(map[string]int64, len(backup.Tables))

	for _, table := range backup.Tables {
		n, countErr := countRows(ctx, conn, table)
		if countErr != nil {
			return backup.Summary{}, countErr
		}

		counts[table] = n
	}

	blobs, unverified, err := blobRefs(ctx, conn)
	if err != nil {
		return backup.Summary{}, err
	}

	return backup.Summary{
		Dialect:              backup.DialectSQLite,
		PrimaryInstallations: installations,
		WrapperIDs:           wrappers,
		RowCounts:            counts,
		Blobs:                blobs,
		Unverified:           unverified,
		SchemaVersion:        int(version),
	}, nil
}

// schemaVersion returns the highest applied migration version.
func schemaVersion(ctx context.Context, conn *sql.DB) (int64, error) {
	const query = `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`

	var version int64

	if err := conn.QueryRowContext(ctx, query).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}

	return version, nil
}

// stringColumn runs a fixed single-column query and returns every value,
// never nil.
func stringColumn(ctx context.Context, conn *sql.DB, query string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read backup summary: %w", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	out := []string{}

	for rows.Next() {
		var value string

		if scanErr := rows.Scan(&value); scanErr != nil {
			return nil, fmt.Errorf("read backup summary: %w", scanErr)
		}

		out = append(out, value)
	}

	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("read backup summary: %w", iterErr)
	}

	return out, nil
}

// countRows returns the row count of one backup table. The table comes from
// backup.Tables, never from outside input.
func countRows(ctx context.Context, conn *sql.DB, table string) (int64, error) {
	var n int64

	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+table+`"`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count rows of %s: %w", table, err)
	}

	return n, nil
}

// blobRefs reads the blob references of a snapshot: the content key, size and
// MD5 of every finalized state version that names content, with the blob key, size and
// MD5 of every checkpoint kept in blobs and every journal batch, and the wrapped key blob of every item key that is not
// shredded, with the blobs of job plans, of job log chunks and of registry
// releases (see addStoredBlobRefs), ordered by key. Size and MD5 stay
// those of the plain bytes, as the state version stores them; sealed versions
// mark their references sealed, so the backup opens them to check and stores
// the sealed bytes. A pending version has no committed content, so the backup
// leaves pending rows out.
func blobRefs(ctx context.Context, conn *sql.DB) ([]backup.BlobRef, []string, error) {
	const query = `SELECT content_key, size, md5, sealed FROM state_versions WHERE content_key <> '' AND status = 'finalized'
		UNION ALL
		SELECT blob_key AS content_key, blob_size AS size, blob_md5 AS md5, 0 AS sealed FROM checkpoints
		UNION ALL
		SELECT blob_key AS content_key, size AS size, md5 AS md5, 0 AS sealed FROM update_journal
		UNION ALL
		SELECT 'namespaces/' || namespace_id || '/item-keys/' || id || '/v' || CAST(blob_version AS TEXT)
			|| '-' || blob_attempt AS content_key,
			blob_size AS size, blob_md5 AS md5, 0 AS sealed FROM item_keys WHERE shredded_at_ms = 0
		ORDER BY content_key`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, nil, fmt.Errorf("read backup blob references: %w", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	out := []backup.BlobRef{}

	for rows.Next() {
		var (
			key    string
			size   int64
			raw    []byte
			sealed bool
		)

		if scanErr := rows.Scan(&key, &size, &raw, &sealed); scanErr != nil {
			return nil, nil, fmt.Errorf("read backup blob references: %w", scanErr)
		}

		if len(raw) != md5.Size {
			return nil, nil, fmt.Errorf(
				"%w: blob with key %q has an MD5 of %d bytes",
				codec.ErrCorruptRow,
				key,
				len(raw),
			)
		}

		var sum [md5.Size]byte

		copy(sum[:], raw)
		out = append(out, backup.BlobRef{MD5: sum, Key: key, Size: size, Sealed: sealed})
	}

	if iterErr := rows.Err(); iterErr != nil {
		return nil, nil, fmt.Errorf("read backup blob references: %w", iterErr)
	}

	collector := &blobrefs.Collector{}
	if addErr := addStoredBlobRefs(ctx, conn, collector); addErr != nil {
		return nil, nil, addErr
	}

	out = append(out, collector.Refs()...)
	slices.SortFunc(out, func(a, b backup.BlobRef) int { return strings.Compare(a.Key, b.Key) })

	return out, collector.Unverified(), nil
}

// snapshotPath reserves a new file name for a VACUUM INTO target in dir: it
// creates and removes a temp file, because VACUUM INTO refuses a target that
// exists.
func snapshotPath(dir string) (string, error) {
	f, err := os.CreateTemp(dir, "dustpan-backup-*.db")
	if err != nil {
		return "", fmt.Errorf("reserve snapshot file: %w", err)
	}

	name := f.Name()

	if err := f.Close(); err != nil {
		return "", fmt.Errorf("reserve snapshot file: %w", errors.Join(err, os.Remove(name)))
	}

	if err := os.Remove(name); err != nil {
		return "", fmt.Errorf("reserve snapshot file: %w", err)
	}

	return name, nil
}

// removeSnapshot removes a snapshot file with its WAL sidecars, if present.
func removeSnapshot(path string) {
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Remove(name) //nolint:errcheck // Best-effort cleanup of scratch files.
	}
}

// removeSnapshotErr removes a snapshot file with its WAL sidecars. A missing
// file is not an error, so discarding an already-removed staging area
// succeeds; any other remove error reports and the caller retains the path
// for retry.
func removeSnapshotErr(path string) error {
	var errs []error

	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// quoteLiteral quotes s as a SQL string literal. VACUUM INTO takes no bound
// parameter, so the snapshot path reaches it as a literal.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// corrupt wraps a staged-backup failure as ErrCorrupt, keeping the cause.
func corrupt(err error) error {
	return errors.Join(err, backup.ErrCorrupt)
}

// createBackupConflict answers why CreateBackup inserted no row: the ID is
// taken (ErrAlreadyExists), or the unique index on running backups refused a
// second running backup (ErrAlreadyRunning).
func (s *Store) createBackupConflict(ctx context.Context, id backup.ID) error {
	return backupConflict(ctx, s.queries, id)
}

// backupConflict is createBackupConflict for the queries of one transaction.
func backupConflict(ctx context.Context, q *db.Queries, id backup.ID) error {
	_, err := q.GetBackup(ctx, id.String())
	if err == nil {
		return backup.ErrAlreadyExists
	}

	if errors.Is(err, sql.ErrNoRows) {
		return backup.ErrAlreadyRunning
	}

	return fmt.Errorf("select backup: %w", err)
}

// checkStagedFeatureRows checks the rows of the staged features that have rules
// of their own: the drift results, the unit settings, the registry, the Git host connections,
// the notification sinks, the triggers, the self-service blueprints, the trust rules and the
// break-glass reviews.
func checkStagedFeatureRows(ctx context.Context, conn *sql.DB) error {
	if err := checkStagedDriftResults(ctx, conn); err != nil {
		return err
	}

	if err := checkStagedJobs(ctx, conn); err != nil {
		return err
	}

	if err := checkStagedUnitSettings(ctx, conn); err != nil {
		return err
	}

	if err := checkStagedRegistry(ctx, conn); err != nil {
		return err
	}

	if err := checkStagedGitHost(ctx, conn); err != nil {
		return err
	}

	if err := checkStagedSinks(ctx, conn); err != nil {
		return err
	}

	if err := checkStagedNotifications(ctx, conn); err != nil {
		return err
	}

	if err := checkStagedTriggers(ctx, conn); err != nil {
		return err
	}

	if err := checkStagedSelfServiceBlueprints(ctx, conn); err != nil {
		return err
	}

	if err := checkStagedTrustRules(ctx, conn); err != nil {
		return err
	}

	if err := checkStagedBreakGlassReviews(ctx, conn); err != nil {
		return err
	}

	return checkStagedForgeLinks(ctx, conn)
}
