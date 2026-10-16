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
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
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
		EndedAtMs:   row.EndedAtMs,
		Target:      row.Target,
		Error:       row.Error,
	})
	if err != nil {
		return fmt.Errorf("insert backup: %w", err)
	}

	if n == 0 {
		return backup.ErrAlreadyExists
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

// FinishBackup implements backup.Registry: it moves the running backup id to
// state with the error text and the end time.
//
//nolint:dupl // The backup and restore registries mirror by design: separate tables with distinct domain types.
func (s *Store) FinishBackup(
	ctx context.Context, id backup.ID, state backup.State, errText string, endedAt time.Time,
) (backup.Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, err := s.fetchBackup(ctx, id); err != nil {
		return backup.Record{}, err
	}

	if state != backup.StateSucceeded && state != backup.StateFailed {
		return backup.Record{}, backup.ErrNotRunning
	}

	n, err := s.queries.FinishBackup(ctx, db.FinishBackupParams{
		State:     string(state),
		Error:     errText,
		EndedAtMs: codec.OptionalMillis(endedAt),
		ID:        id.String(),
	})
	if err != nil {
		return backup.Record{}, fmt.Errorf("finish backup: %w", err)
	}

	if n == 0 {
		return backup.Record{}, backup.ErrNotRunning
	}

	return s.fetchBackup(ctx, id)
}

// FailRunningBackups implements backup.Registry: it marks every running row
// failed with the error text and the end time. The server calls it at start,
// after a stop during a backup.
func (s *Store) FailRunningBackups(ctx context.Context, endedAt time.Time, errText string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	err := s.queries.FailRunningBackups(ctx, db.FailRunningBackupsParams{
		Error:     errText,
		EndedAtMs: codec.OptionalMillis(endedAt),
	})
	if err != nil {
		return fmt.Errorf("fail running backups: %w", err)
	}

	return nil
}

// backupFromRow converts a backup row into a domain record. Unknown states
// report a corrupt row: every writer validates the state first.
func backupFromRow(row db.Backup) (backup.Record, error) {
	return codec.FromBackupRow(codec.BackupRow{
		ID:          row.ID,
		State:       row.State,
		Target:      row.Target,
		Error:       row.Error,
		StartedAtMs: row.StartedAtMs,
		EndedAtMs:   row.EndedAtMs,
	})
}

// CheckCoverage implements backup.Coverage: it compares the column list of each
// table of backup.Tables with the columns that the schema holds for the table
// (ADR 42, decision 5). A table without a list, or a column that differs, fails
// with backup.ErrCoverage.
func (s *Store) CheckCoverage(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	lists := make(map[string][]string, len(backup.Tables))

	for _, spec := range sqliteBackupTables() {
		lists[spec.table] = spec.columns
	}

	for _, table := range backup.Tables {
		listed, ok := lists[table]
		if !ok {
			return fmt.Errorf("backup table %q has no column list: %w", table, backup.ErrCoverage)
		}

		live, err := s.liveColumns(ctx, table)
		if err != nil {
			return err
		}

		if checkErr := backup.CheckColumns(table, listed, live); checkErr != nil {
			return checkErr
		}
	}

	return s.checkPortableColumns(ctx)
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
// fails while the two differ. It excludes bookkeeping (schema_migrations)
// and SQLite internals.
func (s *Store) SchemaTables(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	const query = `SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations'
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

// swapDatabase replaces the destination file with the staged file and reopens
// the store on it. The staged file passed every check, and the destination is
// empty, so the rename cannot mix two installations. It removes the stale
// write-ahead log first, so the reopened store never recovers it. It holds
// the store lock exclusive, so concurrent readers wait out the rename instead
// of reading a closed handle.
func (s *Store) swapDatabase(ctx context.Context, temp string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.swapDatabaseLocked(ctx, temp)
}

// swapDatabaseLocked is swapDatabase with the store lock already held: the
// keep-access apply holds the write lock from the live snapshot through the
// swap, so it swaps without taking the lock again.
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

	conn, err := openDatabase(ctx, s.path)
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
	conn, err := openDatabase(ctx, s.path)
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
func checkRestoreFile(ctx context.Context, path string, want backup.Summary) (backup.Summary, error) {
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
	query.Set("mode", "ro")
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
// the schema version, the home regions, the wrapper IDs, the row count of
// each table in backup.Tables, and the blob references. Callers pass a
// snapshot, never the live database, so the counts and the references
// describe one consistent view.
func summarizeConn(ctx context.Context, conn *sql.DB) (backup.Summary, error) {
	version, err := schemaVersion(ctx, conn)
	if err != nil {
		return backup.Summary{}, err
	}

	regions, err := stringColumn(
		ctx,
		conn,
		`SELECT DISTINCT home_region FROM namespaces ORDER BY home_region`,
	)
	if err != nil {
		return backup.Summary{}, err
	}

	wrappers, err := stringColumn(
		ctx,
		conn,
		`SELECT DISTINCT wrapper_id FROM secret_keys ORDER BY wrapper_id`,
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

	blobs, err := blobRefs(ctx, conn)
	if err != nil {
		return backup.Summary{}, err
	}

	return backup.Summary{
		Dialect:       backup.DialectSQLite,
		HomeRegions:   regions,
		WrapperIDs:    wrappers,
		RowCounts:     counts,
		Blobs:         blobs,
		SchemaVersion: int(version),
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
// MD5 of every state version that names content, with the blob key, size and
// MD5 of every checkpoint kept in blobs and every journal batch, ordered by key. Size and MD5 stay
// those of the plain bytes, as the state version stores them; sealed versions
// mark their references sealed, so the backup opens them to check and stores
// the sealed bytes. A pending version names no content, so only finalized
// versions appear.
func blobRefs(ctx context.Context, conn *sql.DB) ([]backup.BlobRef, error) {
	const query = `SELECT content_key, size, md5, sealed FROM state_versions WHERE content_key <> ''
		UNION ALL
		SELECT blob_key AS content_key, blob_size AS size, blob_md5 AS md5, 0 AS sealed FROM checkpoints
		WHERE blob_key IS NOT NULL AND blob_key <> ''
		UNION ALL
		SELECT blob_key AS content_key, size AS size, md5 AS md5, 0 AS sealed FROM update_journal
		ORDER BY content_key`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read backup blob references: %w", err)
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
			return nil, fmt.Errorf("read backup blob references: %w", scanErr)
		}

		if len(raw) != md5.Size {
			return nil, fmt.Errorf(
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
		return nil, fmt.Errorf("read backup blob references: %w", iterErr)
	}

	return out, nil
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
