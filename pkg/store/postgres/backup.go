package postgres

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var (
	_ backup.Exporter  = (*Store)(nil)
	_ backup.Importer  = (*Store)(nil)
	_ backup.Registry  = (*Store)(nil)
	_ backup.Inspector = (*Store)(nil)
	_ backup.KeyPeeker = (*Store)(nil)
	_ backup.Stager    = (*Store)(nil)
)

// pgDumpVersion is the version of the export stream below. Import rejects any
// other version as corrupt.
const pgDumpVersion = 1

// pgMD5Len is the length of an MD5 checksum in bytes.
const pgMD5Len = 16

// COPY column names shared by several backup tables.
const (
	pgColCreatedAtMs = "created_at_ms"
	pgColName        = "name"
	pgColNamespace   = "namespace"
	pgColSequence    = "sequence"
	pgColStackID     = "stack_id"
	pgColStatus      = "status"
	pgColUpdateID    = "update_id"
	pgColVersion     = "version"
)

// pgTableDump is one table of the export stream: the raw COPY text of its
// rows and the row count COPY reported while capturing.
type pgTableDump struct {
	Table string
	Copy  []byte
	Rows  int64
}

// pgDump is the PostgreSQL export stream: one entry per table of
// backup.Tables in foreign-key order, with the SHA-256 over the concatenated
// Copy bytes of every table in order. The stream format is internal (ADR 30
// decision 6): the manifest layer records the checksum with the SQL part, and
// Import rejects bytes that do not match it.
type pgDump struct {
	Tables  []pgTableDump
	SHA256  []byte
	Version int
}

// pgQuerier is the QueryRow of a pool or a transaction, for the hand-written
// backup queries whose table names come from the allowlist backup.Tables.
type pgQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// pgCopyTable describes the COPY columns of one backup table: the columns to
// dump in order, and the key prefix that orders the dump deterministically.
type pgCopyTable struct {
	columns []string
	orderBy []string
	table   string
}

// pgCopyTables lists the COPY columns of every table of backup.Tables, in
// foreign-key order: namespaces first, state_versions after workspaces.
// Coverage and load order come from backup.Tables alone; this holds only
// column metadata. pgCopySpec reports an error for a table missing here, so a
// new table in backup.Tables breaks loudly until its columns land here.
func pgCopyTables() []pgCopyTable {
	tables := append([]pgCopyTable{}, pgCopyStackTables()...)
	tables = append(tables, pgCopyScopeTables()...)
	tables = append(tables, pgCopyWorkspaceTables()...)

	return tables
}

// pgCopyStackTables holds the COPY columns of the namespace and stack
// tables, in foreign-key order.
func pgCopyStackTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table:   "namespaces",
			columns: []string{pgColName, "home_region", "failover_version", pgColCreatedAtMs},
			orderBy: []string{pgColName},
		},
		{
			table: "stacks",
			columns: []string{
				pgColNamespace, "project", pgColName, "id", pgColVersion, pgColCreatedAtMs, "updated_at_ms",
			},
			orderBy: []string{pgColNamespace, "project", pgColName},
		},
		{
			table:   "stack_tags",
			columns: []string{pgColStackID, pgColName, "value"},
			orderBy: []string{pgColStackID, pgColName},
		},
		{
			table: "updates",
			columns: []string{
				pgColStackID, "id", "kind", "dry_run", pgColStatus, pgColVersion, "revision",
				"lease_digest", "lease_expires_at_ms", pgColCreatedAtMs, "started_at_ms", "ended_at_ms",
			},
			orderBy: []string{pgColStackID, "id"},
		},
		{
			table: "checkpoints",
			columns: []string{
				pgColStackID, pgColUpdateID, "lease_digest", "schema_version", pgColSequence,
				"digest", "invalid", "features", "deployment", "saved_at_ms",
				"blob_key", "size", "blob_size", "blob_md5",
			},
			orderBy: []string{pgColStackID, pgColUpdateID},
		},
		{
			table:   "update_events",
			columns: []string{pgColStackID, pgColUpdateID, pgColSequence, "timestamp_ms", "payload"},
			orderBy: []string{pgColStackID, pgColUpdateID, pgColSequence},
		},
	}
}

// pgCopyScopeTables holds the COPY columns of the secret and inventory
// tables, in foreign-key order.
func pgCopyScopeTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "secret_keys",
			columns: []string{
				pgColNamespace, pgColStackID, "wrapper_id", "ciphertext", pgColCreatedAtMs,
			},
			orderBy: []string{pgColNamespace, pgColStackID},
		},
		{
			table: "inventory",
			columns: []string{
				pgColNamespace, "tool", pgColName, "unit_id", "display_name",
				"last_changed_ms", pgColStatus, "resource_count",
			},
			orderBy: []string{pgColNamespace, "tool", pgColName},
		},
	}
}

// pgCopyWorkspaceTables holds the COPY columns of the workspace, backup and
// installation tables, in foreign-key order. The installation table has no
// references, so it loads last.
func pgCopyWorkspaceTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "workspaces",
			columns: []string{
				"id", pgColNamespace, pgColName, "revision", "lock_id", "lock_holder",
				"lock_reason", "locked_at_ms", "current_state_version_id", pgColCreatedAtMs, "updated_at_ms",
			},
			orderBy: []string{"id"},
		},
		{
			table: "state_versions",
			columns: []string{
				"id", "workspace_id", pgColStatus, "serial", "lineage", "md5", "size",
				"content_key", "outputs", "force", "upload_digest", "upload_expires_at_ms",
				"holder", "lock_id", pgColCreatedAtMs, "finalized_at_ms",
			},
			orderBy: []string{"id"},
		},
		{
			table: "audit_entries",
			columns: []string{
				pgColNamespace, pgColSequence, "subject", "idp_subject", "token_id", "action",
				"object", pgColVersion, "value_path", "at_ms", "source_ip", "user_agent",
				"channel", "kind", "outcome", "request_id", "prev_hash", "hash",
			},
			orderBy: []string{pgColNamespace, pgColSequence},
		},
		{
			table:   "backups",
			columns: []string{"id", "state", "started_at_ms", "ended_at_ms", "target", "error"},
			orderBy: []string{"id"},
		},
		{
			table:   "installation",
			columns: []string{"id", "state"},
			orderBy: []string{"id"},
		},
	}
}

// pgCopySpec returns the COPY description of table, which must be in
// backup.Tables.
func pgCopySpec(table string) (pgCopyTable, error) {
	for _, spec := range pgCopyTables() {
		if spec.table == table {
			return spec, nil
		}
	}

	return pgCopyTable{}, fmt.Errorf("backup table %q: %w", table, backup.ErrCorrupt)
}

// pgIdent quotes one identifier. Table and column names come from the
// constant spec above, never from the network.
func pgIdent(name string) string {
	return `"` + name + `"`
}

// pgColumns joins quoted identifiers for a COPY column list.
func pgColumns(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, pgIdent(name))
	}

	return strings.Join(quoted, ", ")
}

// pgCopyTo returns the COPY statement that dumps table in a stable row order.
func pgCopyTo(table string, columns, orderBy []string) string {
	return "COPY (SELECT " + pgColumns(columns) + " FROM " + pgIdent(table) +
		" ORDER BY " + pgColumns(orderBy) + ") TO STDOUT"
}

// pgCopyFrom returns the COPY statement that loads table.
func pgCopyFrom(table string, columns []string) string {
	return "COPY " + pgIdent(table) + " (" + pgColumns(columns) + ") FROM STDIN"
}

// Export implements backup.Exporter: it captures every table in one
// repeatable-read, read-only transaction, so concurrent writes land all in
// the capture or all out of it.
//
// The dump uses COPY text, not COPY (FORMAT binary): every column type of the
// schema (text, bigint, boolean, bytea, jsonb) round-trips through COPY text
// symmetrically, the output stays stable across server versions and readable
// while debugging, and binary would bind the stream to server-version details
// for no gain on these small metadata rows. No pg_dump runs: the image is
// distroless static (ADR 27).
func (s *Store) Export(ctx context.Context, w io.Writer) (backup.Summary, error) {
	captured, err := s.capture(ctx)
	if err != nil {
		return backup.Summary{}, err
	}

	if err := gob.NewEncoder(w).Encode(captured.dump); err != nil {
		return backup.Summary{}, fmt.Errorf("encode backup stream: %w", err)
	}

	return captured.summary, nil
}

// pgCapture is the result of one capture transaction: the stream and the
// summary of what it holds.
type pgCapture struct {
	dump    pgDump
	summary backup.Summary
}

// inBackupTx runs fn in one transaction with opts, committing on success and
// rolling back on failure. Callers pass explicit isolation and access mode
// for the capture, and the defaults for the load.
func inBackupTx(ctx context.Context, pool *pgxpool.Pool, opts pgx.TxOptions, fn func(tx pgx.Tx) error) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire backup connection: %w", err)
	}

	defer conn.Release()

	tx, err := conn.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("begin backup transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			return errors.Join(err, fmt.Errorf("roll back backup transaction: %w", rbErr))
		}

		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit backup transaction: %w", err)
	}

	return nil
}

// capture reads every table with COPY through pgx in one repeatable-read,
// read-only transaction. Row counts come from COPY itself, inside the same
// transaction as the rows.
func (s *Store) capture(ctx context.Context) (pgCapture, error) {
	var dumps []pgTableDump

	var counts map[string]int64

	var attrs pgAttrs

	//nolint:exhaustruct_v5 // The capture needs only isolation and access mode; deferrable stays off.
	opts := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}

	err := inBackupTx(ctx, s.pool, opts, func(tx pgx.Tx) error {
		var err error

		dumps, counts, err = copyTablesOut(ctx, tx)
		if err != nil {
			return err
		}

		attrs, err = backupAttrs(ctx, s.queries.WithTx(tx), tx)

		return err
	})
	if err != nil {
		return pgCapture{}, err
	}

	sum, err := pgChecksum(dumps)
	if err != nil {
		return pgCapture{}, err
	}

	return pgCapture{
		dump: pgDump{Tables: dumps, SHA256: sum, Version: pgDumpVersion},
		summary: backup.Summary{
			Dialect:       backup.DialectPostgres,
			HomeRegions:   attrs.regions,
			WrapperIDs:    attrs.wrappers,
			RowCounts:     counts,
			Blobs:         attrs.blobs,
			SchemaVersion: attrs.version,
		},
	}, nil
}

// copyTablesOut dumps every table of backup.Tables in order and returns the
// dumps with the row count of each.
func copyTablesOut(ctx context.Context, tx pgx.Tx) ([]pgTableDump, map[string]int64, error) {
	dumps := make([]pgTableDump, 0, len(backup.Tables))
	counts := make(map[string]int64, len(backup.Tables))

	for _, table := range backup.Tables {
		dump, err := copyTableOut(ctx, tx, table)
		if err != nil {
			return nil, nil, err
		}

		dumps = append(dumps, dump)
		counts[table] = dump.Rows
	}

	return dumps, counts, nil
}

// copyTableOut dumps one table with COPY through pgx. The returned row count
// is what COPY reported, counted inside the capture transaction.
func copyTableOut(ctx context.Context, tx pgx.Tx, table string) (pgTableDump, error) {
	spec, err := pgCopySpec(table)
	if err != nil {
		return pgTableDump{}, err
	}

	var buf bytes.Buffer

	tag, err := tx.Conn().PgConn().CopyTo(ctx, &buf, pgCopyTo(table, spec.columns, spec.orderBy))
	if err != nil {
		return pgTableDump{}, fmt.Errorf("copy table %s: %w", table, err)
	}

	return pgTableDump{Table: table, Copy: buf.Bytes(), Rows: tag.RowsAffected()}, nil
}

// pgChecksum returns the SHA-256 over the concatenated COPY bytes of dumps
// in order: the checksum the manifest records for the SQL part.
func pgChecksum(dumps []pgTableDump) ([]byte, error) {
	summer := sha256.New()

	for _, dump := range dumps {
		// hash.Hash.Write never returns an error; the check satisfies errcheck.
		if _, err := summer.Write(dump.Copy); err != nil {
			return nil, fmt.Errorf("hash backup stream: %w", err)
		}
	}

	return summer.Sum(nil), nil
}

// pgAttrs is the manifest subset the SQL data determines, without row counts.
type pgAttrs struct {
	regions  []string
	wrappers []string
	blobs    []backup.BlobRef
	version  int
}

// backupAttrs reads the home regions, wrapper IDs, blob references and
// schema version. Export calls it in the capture transaction, so the values
// match the captured rows; summarize calls it after the load.
func backupAttrs(ctx context.Context, q *db.Queries, qr pgQuerier) (pgAttrs, error) {
	regions, err := q.BackupHomeRegions(ctx)
	if err != nil {
		return pgAttrs{}, fmt.Errorf("select backup regions: %w", err)
	}

	wrappers, err := q.BackupWrapperIDs(ctx)
	if err != nil {
		return pgAttrs{}, fmt.Errorf("select backup wrappers: %w", err)
	}

	rows, err := q.BackupBlobs(ctx)
	if err != nil {
		return pgAttrs{}, fmt.Errorf("select backup blobs: %w", err)
	}

	blobs := make([]backup.BlobRef, 0, len(rows))

	for _, row := range rows {
		ref, refErr := backupBlobRef(row)
		if refErr != nil {
			return pgAttrs{}, refErr
		}

		blobs = append(blobs, ref)
	}

	var version int64

	// schema_migrations is runtime bookkeeping, not a migration, so no sqlc
	// query covers it; the adapter reads it directly.
	if err := qr.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return pgAttrs{}, fmt.Errorf("read schema version: %w", err)
	}

	if regions == nil {
		regions = []string{}
	}

	if wrappers == nil {
		wrappers = []string{}
	}

	return pgAttrs{regions: regions, wrappers: wrappers, blobs: blobs, version: int(version)}, nil
}

// backupBlobRef converts one blob row into a manifest reference. The MD5
// column always holds 16 bytes; anything else is corrupt data.
func backupBlobRef(row db.BackupBlobsRow) (backup.BlobRef, error) {
	if len(row.Md5) != pgMD5Len {
		return backup.BlobRef{}, fmt.Errorf(
			"backup blob %q holds an MD5 of %d bytes: %w",
			row.ContentKey,
			len(row.Md5),
			backup.ErrCorrupt,
		)
	}

	var md5 [pgMD5Len]byte

	copy(md5[:], row.Md5)

	return backup.BlobRef{MD5: md5, Key: row.ContentKey, Size: row.Size}, nil
}

// Import implements backup.Importer: it checks the manifest, then loads the
// export into an empty store of the same dialect and schema version. It
// returns ErrNotEmpty, ErrDialectMismatch, ErrSchemaMismatch or ErrCorrupt,
// and a failed import leaves an empty store.
func (s *Store) Import(ctx context.Context, r io.Reader, want backup.Summary) (backup.Summary, error) {
	if want.Dialect != backup.DialectPostgres {
		return backup.Summary{}, fmt.Errorf(
			"import backup of dialect %q into postgres: %w",
			want.Dialect,
			backup.ErrDialectMismatch,
		)
	}

	version, err := s.schemaVersion(ctx)
	if err != nil {
		return backup.Summary{}, err
	}

	if want.SchemaVersion != version {
		return backup.Summary{}, fmt.Errorf(
			"import backup of schema version %d into version %d: %w",
			want.SchemaVersion,
			version,
			backup.ErrSchemaMismatch,
		)
	}

	var dump pgDump

	if decodeErr := gob.NewDecoder(r).Decode(&dump); decodeErr != nil {
		return backup.Summary{}, fmt.Errorf("decode backup stream: %w", backup.ErrCorrupt)
	}

	frames, err := pgDumpFrames(dump)
	if err != nil {
		return backup.Summary{}, err
	}

	// The destination already migrated when it opened; migrating again is a
	// no-op that keeps a hand-made empty schema loadable too.
	if err := s.migrate(ctx); err != nil {
		return backup.Summary{}, err
	}

	if err := s.loadDump(ctx, frames, want); err != nil {
		return backup.Summary{}, err
	}

	return s.summarize(ctx)
}

// stagingSchemaPrefix prefixes every staging schema name. The prefix check in
// dropStagingSchema keeps a programming error from dropping a live schema.
const stagingSchemaPrefix = "restore_stage_"

// StageRestore implements backup.Stager: it loads the export into a staging
// schema and checks it (row counts, foreign keys, blob references), without
// touching the live tables. The schema stays as the staging area until
// DiscardStaging drops it; a later change applies it. A failed stage drops the
// schema. Only one staging exists at a time: a new stage drops the previous one.
func (s *Store) StageRestore(
	ctx context.Context, r io.Reader, want backup.Summary,
) (backup.Summary, error) {
	if want.Dialect != backup.DialectPostgres {
		return backup.Summary{}, fmt.Errorf(
			"stage backup of dialect %q into postgres: %w",
			want.Dialect,
			backup.ErrDialectMismatch,
		)
	}

	version, err := s.schemaVersion(ctx)
	if err != nil {
		return backup.Summary{}, err
	}

	if want.SchemaVersion != version {
		return backup.Summary{}, fmt.Errorf(
			"stage backup of schema version %d into version %d: %w",
			want.SchemaVersion,
			version,
			backup.ErrSchemaMismatch,
		)
	}

	var dump pgDump

	if decodeErr := gob.NewDecoder(r).Decode(&dump); decodeErr != nil {
		return backup.Summary{}, fmt.Errorf("decode backup stream: %w", backup.ErrCorrupt)
	}

	frames, framesErr := pgDumpFrames(dump)
	if framesErr != nil {
		return backup.Summary{}, framesErr
	}

	if tablesErr := checkWantTables(want); tablesErr != nil {
		return backup.Summary{}, tablesErr
	}

	staging, err := newStagingSchema()
	if err != nil {
		return backup.Summary{}, err
	}

	return s.buildStaging(ctx, stagingInput{
		staging: staging,
		frames:  frames,
		want:    want,
	})
}

// stagingInput groups what buildStaging needs: the staging schema, the decoded
// tables, and the manifest summary.
type stagingInput struct {
	frames  map[string]pgTableDump
	want    backup.Summary
	staging string
}

// buildStaging creates the staging schema, loads and checks the dump, and keeps
// the schema as the staging area. A failure drops the schema: the live tables
// stay unchanged.
func (s *Store) buildStaging(ctx context.Context, in stagingInput) (backup.Summary, error) {
	kept := false

	defer func() {
		if !kept {
			// Best-effort cleanup of the scratch schema after a failed stage.
			_ = s.dropStagingSchema(context.WithoutCancel(ctx), in.staging) //nolint:errcheck // Best-effort cleanup.
		}
	}()

	if createErr := s.createStagingSchema(ctx, in.staging); createErr != nil {
		return backup.Summary{}, createErr
	}

	if loadErr := s.loadStagingDump(ctx, in.staging, in.frames, in.want); loadErr != nil {
		return backup.Summary{}, loadErr
	}

	sum, err := s.summarizeStaging(ctx, in.staging, in.want)
	if err != nil {
		return backup.Summary{}, err
	}

	if blobErr := checkStagedBlobRefs(sum.Blobs, in.want.Blobs); blobErr != nil {
		return backup.Summary{}, blobErr
	}

	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()

	if s.stagingSchema != "" {
		if dropErr := s.dropStagingSchema(context.WithoutCancel(ctx), s.stagingSchema); dropErr != nil {
			return backup.Summary{}, dropErr
		}
	}

	s.stagingSchema = in.staging
	kept = true

	return sum, nil
}

// DiscardStaging implements backup.Stager: it drops the staging schema, if
// any. It leaves the live tables unchanged.
func (s *Store) DiscardStaging(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("discard staging area: %w", err)
	}

	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()

	if s.stagingSchema == "" {
		return nil
	}

	if err := s.dropStagingSchema(ctx, s.stagingSchema); err != nil {
		return err
	}

	s.stagingSchema = ""

	return nil
}

// stagingRandBytes is the entropy of one staging schema name in bytes, as hex.
const stagingRandBytes = 8

// newStagingSchema returns a fresh staging schema name: the staging prefix
// with hex entropy. The name holds letters, digits and underscores only, so
// pgIdent quotes it safely.
func newStagingSchema() (string, error) {
	raw := make([]byte, stagingRandBytes)

	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("name staging schema: %w", err)
	}

	return stagingSchemaPrefix + hex.EncodeToString(raw), nil
}

// createStagingSchema creates the staging schema and replays every migration
// in it, so its tables with their foreign keys match the live schema. The
// migration DDL runs with the search path on the staging schema only: the
// live tables stay untouched. It records every migration in the staging
// schema_migrations, so the staged data carries its own schema version.
func (s *Store) createStagingSchema(ctx context.Context, staging string) error {
	all, err := migrate.Load(migrations, "migrations")
	if err != nil {
		return err
	}

	if _, err := s.pool.Exec(ctx, "CREATE SCHEMA "+pgIdent(staging)); err != nil {
		return fmt.Errorf("create staging schema: %w", err)
	}

	opts := pgx.TxOptions{
		IsoLevel:       pgx.ReadCommitted,
		AccessMode:     pgx.ReadWrite,
		DeferrableMode: pgx.NotDeferrable,
		BeginQuery:     "",
		CommitQuery:    "",
	}

	return inBackupTx(ctx, s.pool, opts, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+pgIdent(staging)); err != nil {
			return fmt.Errorf("set staging search path: %w", err)
		}

		const createMigrations = `CREATE TABLE schema_migrations (
			version BIGINT NOT NULL PRIMARY KEY,
			name    TEXT   NOT NULL
		)`

		if _, err := tx.Exec(ctx, createMigrations); err != nil {
			return fmt.Errorf("create staging schema_migrations: %w", err)
		}

		for _, m := range all {
			// No arguments, so pgx uses the simple protocol, which allows
			// several statements, as the migration runner does.
			if _, err := tx.Exec(ctx, m.SQL); err != nil {
				return fmt.Errorf("build staging schema at migration %s: %w", m.Name, err)
			}

			const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

			if _, err := tx.Exec(ctx, record, m.Version, m.Name); err != nil {
				return fmt.Errorf("record staging migration %s: %w", m.Name, err)
			}
		}

		return nil
	})
}

// loadStagingDump loads frames into the staging schema in one transaction, in
// the foreign-key order of backup.Tables. The COPY checks enforce the foreign
// keys: a child without its parent fails the load with ErrCorrupt. A failure
// rolls back and leaves no staged rows.
func (s *Store) loadStagingDump(
	ctx context.Context, staging string, frames map[string]pgTableDump, want backup.Summary,
) error {
	opts := pgx.TxOptions{
		IsoLevel:       pgx.ReadCommitted,
		AccessMode:     pgx.ReadWrite,
		DeferrableMode: pgx.NotDeferrable,
		BeginQuery:     "",
		CommitQuery:    "",
	}

	return inBackupTx(ctx, s.pool, opts, func(tx pgx.Tx) error {
		// The unqualified COPY statements below resolve to the staging schema
		// through the search path, as does every foreign key they enforce.
		if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+pgIdent(staging)); err != nil {
			return fmt.Errorf("set staging search path: %w", err)
		}

		// The migration seeds the installation row in the staging schema as
		// it does in a fresh destination. Clear it, as the import does, so
		// the COPY below loads the row the backup holds.
		if _, err := tx.Exec(ctx, `DELETE FROM installation`); err != nil {
			return fmt.Errorf("clear staged installation state: %w", err)
		}

		for _, table := range backup.Tables {
			if err := copyTableIn(ctx, tx, table, frames[table], want); err != nil {
				return err
			}
		}

		return nil
	})
}

// summarizeStaging reads the summary of the staging schema: the dialect, the
// schema version the staging schema_migrations records, the home regions, the
// wrapper IDs, the row count of each table, and the blob references. The live
// tables stay out of every query: each one names the staging schema. A staged
// version that differs from the manifest reports ErrCorrupt.
func (s *Store) summarizeStaging(
	ctx context.Context, staging string, want backup.Summary,
) (backup.Summary, error) {
	regions, err := stagingStrings(ctx, s.pool, staging, "namespaces", "home_region")
	if err != nil {
		return backup.Summary{}, err
	}

	wrappers, err := stagingStrings(ctx, s.pool, staging, "secret_keys", "wrapper_id")
	if err != nil {
		return backup.Summary{}, err
	}

	counts := make(map[string]int64, len(backup.Tables))

	for _, table := range backup.Tables {
		count, countErr := stagingCount(ctx, s.pool, staging, table)
		if countErr != nil {
			return backup.Summary{}, countErr
		}

		counts[table] = count
	}

	blobs, err := stagingBlobRefs(ctx, s.pool, staging)
	if err != nil {
		return backup.Summary{}, err
	}

	version, err := stagingSchemaVersion(ctx, s.pool, staging)
	if err != nil {
		return backup.Summary{}, err
	}

	if version != want.SchemaVersion {
		return backup.Summary{}, fmt.Errorf(
			"staged backup schema version %d differs from manifest %d: %w",
			version,
			want.SchemaVersion,
			backup.ErrCorrupt,
		)
	}

	return backup.Summary{
		Dialect:       backup.DialectPostgres,
		HomeRegions:   regions,
		WrapperIDs:    wrappers,
		RowCounts:     counts,
		Blobs:         blobs,
		SchemaVersion: version,
	}, nil
}

// stagingSchemaVersion returns the highest migration version the staging
// schema records in its own schema_migrations.
func stagingSchemaVersion(ctx context.Context, pool *pgxpool.Pool, staging string) (int, error) {
	var version int64

	query := "SELECT COALESCE(MAX(version), 0) FROM " + stagingTable(staging, "schema_migrations")

	if err := pool.QueryRow(ctx, query).Scan(&version); err != nil {
		return 0, fmt.Errorf("read staged schema version: %w", err)
	}

	return int(version), nil
}

// stagingTable qualifies table in staging for the hand-written staging
// queries. Both names come from constants, never from the network.
func stagingTable(staging, table string) string {
	return pgIdent(staging) + "." + pgIdent(table)
}

// stagingStrings returns the distinct values of column in the staging table,
// ordered, never nil.
func stagingStrings(
	ctx context.Context, pool *pgxpool.Pool, staging, table, column string,
) ([]string, error) {
	rows, err := pool.Query(
		ctx,
		"SELECT DISTINCT "+pgIdent(column)+" FROM "+stagingTable(staging, table)+" ORDER BY "+pgIdent(column),
	)
	if err != nil {
		return nil, fmt.Errorf("read staged backup summary: %w", err)
	}

	defer rows.Close()

	out := []string{}

	for rows.Next() {
		var value string

		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("read staged backup summary: %w", err)
		}

		out = append(out, value)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read staged backup summary: %w", err)
	}

	return out, nil
}

// stagingCount returns the row count of the staging table. The table name
// comes from the allowlist backup.Tables, never from the network.
func stagingCount(ctx context.Context, pool *pgxpool.Pool, staging, table string) (int64, error) {
	var count int64

	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+stagingTable(staging, table)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count staged table %s: %w", table, err)
	}

	return count, nil
}

// stagingBlobRefs reads the blob references of the staging schema: the
// content key, size and MD5 of every state version that names content, ordered
// by key. A pending version names no content, so only finalized versions appear.
func stagingBlobRefs(ctx context.Context, pool *pgxpool.Pool, staging string) ([]backup.BlobRef, error) {
	rows, err := pool.Query(
		ctx,
		"SELECT content_key, size, md5 FROM "+stagingTable(staging, "state_versions")+
			" WHERE content_key <> '' ORDER BY content_key",
	)
	if err != nil {
		return nil, fmt.Errorf("read staged blob references: %w", err)
	}

	defer rows.Close()

	out := []backup.BlobRef{}

	for rows.Next() {
		var (
			key  string
			size int64
			raw  []byte
		)

		if err := rows.Scan(&key, &size, &raw); err != nil {
			return nil, fmt.Errorf("read staged blob references: %w", err)
		}

		if len(raw) != pgMD5Len {
			return nil, fmt.Errorf(
				"staged state version with key %q has an MD5 of %d bytes: %w",
				key,
				len(raw),
				backup.ErrCorrupt,
			)
		}

		var sum [pgMD5Len]byte

		copy(sum[:], raw)
		out = append(out, backup.BlobRef{MD5: sum, Key: key, Size: size})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read staged blob references: %w", err)
	}

	return out, nil
}

// checkStagedBlobRefs compares the blob references of the staged data with the
// manifest: the staged data must name exactly the keys of the manifest, each
// with its size and checksum.
func checkStagedBlobRefs(staged, want []backup.BlobRef) error {
	byKey := func(a, b backup.BlobRef) int { return strings.Compare(a.Key, b.Key) }

	ordered := slices.Clone(staged)
	slices.SortFunc(ordered, byKey)

	wanted := slices.Clone(want)
	slices.SortFunc(wanted, byKey)

	same := func(a, b backup.BlobRef) bool {
		return a.Key == b.Key && a.Size == b.Size && a.MD5 == b.MD5
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

// dropStagingSchema removes the staging schema with its tables. The name comes
// from newStagingSchema, never from the network; the prefix check keeps a
// programming error from dropping a live schema.
func (s *Store) dropStagingSchema(ctx context.Context, staging string) error {
	if !strings.HasPrefix(staging, stagingSchemaPrefix) {
		return fmt.Errorf("drop staging schema %q: %w", staging, backup.ErrCorrupt)
	}

	if _, err := s.pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgIdent(staging)+" CASCADE"); err != nil {
		return fmt.Errorf("drop staging schema: %w", err)
	}

	return nil
}

// PeekKey implements backup.KeyPeeker: it decodes the export stream and reads
// one wrapped data key of wrapperID from the COPY bytes of secret_keys,
// without loading the destination.
func (s *Store) PeekKey(_ context.Context, r io.Reader, wrapperID string) (secret.WrappedKey, error) {
	var none secret.WrappedKey

	var dump pgDump

	if err := gob.NewDecoder(r).Decode(&dump); err != nil {
		return none, fmt.Errorf("decode backup stream: %w", backup.ErrCorrupt)
	}

	if dump.Version != pgDumpVersion {
		return none, fmt.Errorf(
			"decode backup stream of version %d: %w", dump.Version, backup.ErrCorrupt,
		)
	}

	for _, frame := range dump.Tables {
		if frame.Table != "secret_keys" {
			continue
		}

		return peekPGKey(frame.Copy, wrapperID)
	}

	return none, fmt.Errorf(
		"backup holds no data key of wrapper %q: %w", wrapperID, backup.ErrCorrupt,
	)
}

// peekPGKey returns one data key of wrapperID from the COPY text of
// secret_keys. The dump orders rows by namespace and stack ID, so the first
// match is stable.
func peekPGKey(copyBytes []byte, wrapperID string) (secret.WrappedKey, error) {
	var none secret.WrappedKey

	for line := range strings.Lines(string(copyBytes)) {
		row, ok := parsePGKeyLine(line, wrapperID)
		if ok {
			return row, nil
		}
	}

	return none, fmt.Errorf(
		"backup holds no data key of wrapper %q: %w", wrapperID, backup.ErrCorrupt,
	)
}

// parsePGKeyLine parses one COPY line of secret_keys and reports whether it
// carries wrapperID. Columns are namespace, stack_id, wrapper_id, ciphertext
// and created_at_ms. Text fields never hold a tab or newline (they are
// validated IDs), and bytea reads as the escaped hex format \\x.
func parsePGKeyLine(line, wrapperID string) (secret.WrappedKey, bool) {
	var none secret.WrappedKey

	line = strings.TrimSuffix(line, "\n")

	fields := strings.Split(line, "\t")
	if len(fields) != 5 || fields[2] != wrapperID {
		return none, false
	}

	ciphertext, err := parsePGBytea(fields[3])
	if err != nil {
		return none, false
	}

	createdAt, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return none, false
	}

	return codec.FromSecretKeyRow(codec.SecretKeyRow{
		Namespace:   fields[0],
		StackID:     fields[1],
		WrapperID:   fields[2],
		Ciphertext:  ciphertext,
		CreatedAtMs: createdAt,
	}), true
}

// parsePGBytea decodes the COPY text of a bytea column. COPY escapes the
// backslash of the hex format, so ciphertext reads as \\x followed by hex.
func parsePGBytea(s string) ([]byte, error) {
	hexDigits, ok := strings.CutPrefix(s, `\\x`)
	if !ok {
		hexDigits, ok = strings.CutPrefix(s, `\x`)
	}

	if !ok {
		return nil, fmt.Errorf("bytea %q: %w", s, backup.ErrCorrupt)
	}

	raw, err := hex.DecodeString(hexDigits)
	if err != nil {
		return nil, fmt.Errorf("bytea: %w", errors.Join(err, backup.ErrCorrupt))
	}

	return raw, nil
}

// schemaVersion returns the highest applied migration version.
func (s *Store) schemaVersion(ctx context.Context) (int, error) {
	var version int64

	const selectVersion = `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`

	if err := s.pool.QueryRow(ctx, selectVersion).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}

	return int(version), nil
}

// pgDumpFrames validates dump and returns its tables by name: the stream
// version matches, every table of backup.Tables appears exactly once, no
// unknown table appears, and the checksum matches the bytes.
func pgDumpFrames(dump pgDump) (map[string]pgTableDump, error) {
	if dump.Version != pgDumpVersion {
		return nil, fmt.Errorf("decode backup stream of version %d: %w", dump.Version, backup.ErrCorrupt)
	}

	if len(dump.Tables) != len(backup.Tables) {
		return nil, fmt.Errorf(
			"decode backup stream of %d tables, want %d: %w",
			len(dump.Tables),
			len(backup.Tables),
			backup.ErrCorrupt,
		)
	}

	frames := make(map[string]pgTableDump, len(dump.Tables))

	for _, frame := range dump.Tables {
		if !slices.Contains(backup.Tables, frame.Table) {
			return nil, fmt.Errorf("decode backup table %q: %w", frame.Table, backup.ErrCorrupt)
		}

		if _, dup := frames[frame.Table]; dup {
			return nil, fmt.Errorf("decode duplicate backup table %q: %w", frame.Table, backup.ErrCorrupt)
		}

		frames[frame.Table] = frame
	}

	sum, err := pgChecksum(dump.Tables)
	if err != nil {
		return nil, err
	}

	if !bytes.Equal(sum, dump.SHA256) {
		return nil, fmt.Errorf("backup stream checksum differs from manifest: %w", backup.ErrCorrupt)
	}

	return frames, nil
}

// loadDump loads frames into an empty store in one transaction, in the
// foreign-key order of backup.Tables: namespaces first, then stacks, tags,
// updates, checkpoints, events, keys, inventory, workspaces, state_versions
// and backups, so parents exist before children reference them. A failure
// rolls back and leaves an empty database.
func (s *Store) loadDump(ctx context.Context, frames map[string]pgTableDump, want backup.Summary) error {
	opts := pgx.TxOptions{
		IsoLevel:       pgx.ReadCommitted,
		AccessMode:     pgx.ReadWrite,
		DeferrableMode: pgx.NotDeferrable,
		BeginQuery:     "",
		CommitQuery:    "",
	}

	return inBackupTx(ctx, s.pool, opts, func(tx pgx.Tx) error {
		total, err := countBackupRows(ctx, tx)
		if err != nil {
			return err
		}

		if total > 0 {
			return backup.ErrNotEmpty
		}

		if err := checkWantTables(want); err != nil {
			return err
		}

		// The migration seeds the installation row, so a fresh destination
		// holds one. It is bookkeeping, not data: clear it before the load,
		// so the COPY below inserts the row the backup holds instead of
		// conflicting with the seed.
		if _, err := tx.Exec(ctx, `DELETE FROM installation`); err != nil {
			return fmt.Errorf("clear installation state: %w", err)
		}

		for _, table := range backup.Tables {
			if err := copyTableIn(ctx, tx, table, frames[table], want); err != nil {
				return err
			}
		}

		return nil
	})
}

// copyTableIn loads one table with COPY through pgx and compares the loaded
// row count with the stream and the manifest.
func copyTableIn(ctx context.Context, tx pgx.Tx, table string, frame pgTableDump, want backup.Summary) error {
	spec, err := pgCopySpec(table)
	if err != nil {
		return err
	}

	tag, err := tx.Conn().PgConn().CopyFrom(ctx, bytes.NewReader(frame.Copy), pgCopyFrom(table, spec.columns))
	if err != nil {
		return fmt.Errorf("load table %s: %w", table, backup.ErrCorrupt)
	}

	if tag.RowsAffected() != frame.Rows {
		return fmt.Errorf(
			"load table %s of %d rows, stream holds %d: %w",
			table,
			tag.RowsAffected(),
			frame.Rows,
			backup.ErrCorrupt,
		)
	}

	if tag.RowsAffected() != want.RowCounts[table] {
		return fmt.Errorf(
			"load table %s of %d rows, manifest holds %d: %w",
			table,
			tag.RowsAffected(),
			want.RowCounts[table],
			backup.ErrCorrupt,
		)
	}

	return nil
}

// checkWantTables rejects a manifest that names a table outside backup.Tables
// or omits one: every table must be present, so an omitted empty table cannot
// slip through the count check.
func checkWantTables(want backup.Summary) error {
	for table := range want.RowCounts {
		if !slices.Contains(backup.Tables, table) {
			return fmt.Errorf("import manifest names unknown table %q: %w", table, backup.ErrCorrupt)
		}
	}

	for _, table := range backup.Tables {
		if _, ok := want.RowCounts[table]; !ok {
			return fmt.Errorf("import manifest omits table %q: %w", table, backup.ErrCorrupt)
		}
	}

	return nil
}

// countBackupRows returns the total rows of every backup table. The seeded
// installation row is bookkeeping, not data, so it never counts: a fresh
// database still counts as empty. Callers run it in the load transaction,
// so the empty check and the load are one atomic step: a concurrent writer
// lands before the check and refuses the import, never inside the loaded data.
func countBackupRows(ctx context.Context, qr pgQuerier) (int64, error) {
	var total int64

	for _, table := range backup.Tables {
		if table == "installation" {
			continue
		}

		count, err := countBackupTable(ctx, qr, table)
		if err != nil {
			return 0, err
		}

		total += count
	}

	return total, nil
}

// countBackupTable returns the rows of one backup table. The table name comes
// from the allowlist backup.Tables, never from the network.
func countBackupTable(ctx context.Context, qr pgQuerier, table string) (int64, error) {
	var count int64

	if err := qr.QueryRow(ctx, "SELECT COUNT(*) FROM "+pgIdent(table)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count table %s: %w", table, err)
	}

	return count, nil
}

// summarize builds the summary of the store: the dialect, the schema
// version, the home regions, wrapper IDs, row counts and blob references.
// Import calls it after the load commits, outside a transaction: the caller
// owns the empty destination, so no concurrent writer can skew the counts.
func (s *Store) summarize(ctx context.Context) (backup.Summary, error) {
	attrs, err := backupAttrs(ctx, s.queries, s.pool)
	if err != nil {
		return backup.Summary{}, err
	}

	counts := make(map[string]int64, len(backup.Tables))

	for _, table := range backup.Tables {
		count, err := countBackupTable(ctx, s.pool, table)
		if err != nil {
			return backup.Summary{}, err
		}

		counts[table] = count
	}

	return backup.Summary{
		Dialect:       backup.DialectPostgres,
		HomeRegions:   attrs.regions,
		WrapperIDs:    attrs.wrappers,
		RowCounts:     counts,
		Blobs:         attrs.blobs,
		SchemaVersion: attrs.version,
	}, nil
}

// CreateBackup implements backup.Registry: it stores r as a running backup
// and reports ErrAlreadyExists when the ID is taken.
func (s *Store) CreateBackup(ctx context.Context, r backup.Record) error {
	n, err := s.queries.CreateBackup(ctx, db.CreateBackupParams{
		ID:          r.ID.String(),
		State:       string(r.State),
		StartedAtMs: codec.Millis(r.StartedAt),
		EndedAtMs:   backupEndedAt(r.EndedAt),
		Target:      r.Target,
		Error:       r.Error,
	})
	if err != nil {
		return fmt.Errorf("insert backup: %w", err)
	}

	if n == 0 {
		return backup.ErrAlreadyExists
	}

	return nil
}

// FinishBackup implements backup.Registry: it moves the running backup id to
// a finished state with the error text and end time, and returns the
// finished row. Any state but succeeded or failed reports ErrNotRunning, as
// do a missing row (ErrNotFound) and a row that is not running.
func (s *Store) FinishBackup(
	ctx context.Context, id backup.ID, state backup.State, errText string, endedAt time.Time,
) (backup.Record, error) {
	if state != backup.StateSucceeded && state != backup.StateFailed {
		return backup.Record{}, backup.ErrNotRunning
	}

	n, err := s.queries.FinishBackup(ctx, db.FinishBackupParams{
		State:     string(state),
		Error:     errText,
		EndedAtMs: backupEndedAt(endedAt),
		ID:        id.String(),
	})
	if err != nil {
		return backup.Record{}, fmt.Errorf("finish backup: %w", err)
	}

	if n == 0 {
		return s.finishFailure(ctx, id)
	}

	return s.GetBackup(ctx, id)
}

// finishFailure reports why FinishBackup changed nothing: ErrNotFound when id
// does not exist, ErrNotRunning when the row is not running.
func (s *Store) finishFailure(ctx context.Context, id backup.ID) (backup.Record, error) {
	if _, err := s.GetBackup(ctx, id); err != nil {
		return backup.Record{}, err
	}

	return backup.Record{}, backup.ErrNotRunning
}

// GetBackup implements backup.Registry: it returns the backup row id, or
// ErrNotFound.
func (s *Store) GetBackup(ctx context.Context, id backup.ID) (backup.Record, error) {
	row, err := s.queries.GetBackup(ctx, id.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return backup.Record{}, backup.ErrNotFound
	}

	if err != nil {
		return backup.Record{}, fmt.Errorf("select backup: %w", err)
	}

	return backupRecord(row), nil
}

// ListBackups implements backup.Registry: every backup row, newest first.
func (s *Store) ListBackups(ctx context.Context) ([]backup.Record, error) {
	rows, err := s.queries.ListBackups(ctx)
	if err != nil {
		return nil, fmt.Errorf("select backups: %w", err)
	}

	out := make([]backup.Record, 0, len(rows))
	for _, row := range rows {
		out = append(out, backupRecord(row))
	}

	return out, nil
}

// FailRunningBackups implements backup.Registry: it marks every running row
// failed with the error text and end time. The server calls it at start,
// after a stop during a backup. Open does not call it: a reconnect must
// preserve running rows, which the conformance suite replays.
func (s *Store) FailRunningBackups(ctx context.Context, endedAt time.Time, errText string) error {
	params := db.FailRunningBackupsParams{Error: errText, EndedAtMs: backupEndedAt(endedAt)}

	if err := s.queries.FailRunningBackups(ctx, params); err != nil {
		return fmt.Errorf("fail running backups: %w", err)
	}

	return nil
}

// backupRecord converts a backup row into a domain record. A NULL end time
// reads as the zero time: the backup still runs.
func backupRecord(row db.Backup) backup.Record {
	var endedAt time.Time

	if row.EndedAtMs.Valid {
		endedAt = codec.FromMillis(row.EndedAtMs.Int64)
	}

	return backup.Record{
		StartedAt: codec.FromMillis(row.StartedAtMs),
		EndedAt:   endedAt,
		ID:        backup.ID(row.ID),
		Target:    row.Target,
		Error:     row.Error,
		State:     backup.State(row.State),
	}
}

// backupEndedAt encodes the end time for the backups table: the zero time is
// NULL, meaning the backup still runs.
func backupEndedAt(endedAt time.Time) pgtype.Int8 {
	if endedAt.IsZero() {
		return pgtype.Int8{Int64: 0, Valid: false}
	}

	return pgtype.Int8{Int64: codec.Millis(endedAt), Valid: true}
}

// SchemaTables implements backup.Inspector: it reads the table catalog, so a
// table missing from backup.Tables fails the conformance suite. Bookkeeping
// (schema_migrations) is not data and stays out.
func (s *Store) SchemaTables(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT table_name FROM information_schema.tables`+
		` WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`+
		` AND table_name <> 'schema_migrations' ORDER BY table_name`)
	if err != nil {
		return nil, fmt.Errorf("select schema tables: %w", err)
	}

	defer rows.Close()

	var out []string

	for rows.Next() {
		var table string

		if err := rows.Scan(&table); err != nil {
			return nil, fmt.Errorf("scan schema table: %w", err)
		}

		out = append(out, table)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schema tables: %w", err)
	}

	return out, nil
}
