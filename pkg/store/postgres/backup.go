package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
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
	"opendustpan.org/opendustpan/pkg/workspace"
)

var (
	_ backup.Registry  = (*Store)(nil)
	_ backup.Inspector = (*Store)(nil)
	_ backup.KeyPeeker = (*Store)(nil)
)

// pgMD5Len is the length of an MD5 checksum in bytes.
const pgMD5Len = 16

// COPY column names shared by several backup tables.
const (
	pgColArrival     = "arrival"
	pgColUnitID      = "unit_id"
	pgColNameKey     = "name_key"
	pgColCreatedAtMs = "created_at_ms"
	pgColCreatedBy   = "created_by"
	pgColDigest      = "digest"
	pgColEndedAtMs   = "ended_at_ms"
	pgColName        = "name"
	pgColNamespace   = "namespace"
	pgColSequence    = "sequence"
	pgColSize        = "size"
	pgColStackID     = "stack_id"
	pgColStartedAtMs = "started_at_ms"
	pgColState       = "state"
	pgColStatus      = "status"
	pgColUpdateID    = "update_id"
	pgColVersion     = "version"
	pgColUpdatedAtMs = "updated_at_ms"
	pgColDeletedAtMs = "deleted_at_ms"
	pgColSource      = "source"
	pgColKind        = "kind"
	pgColSubject     = "subject"
	pgColDeletedName = "deleted_name"
	pgColTool        = "tool"
	pgColPrincipalID = "principal_id"
	pgColDisplayName = "display_name"
	pgColProviderID  = "provider_id"
	pgColSink        = "sink"
	pgColExpiresAtMs = "expires_at_ms"
)

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
	tables = append(tables, pgCopyIdentityTables()...)
	tables = append(tables, pgCopySessionTables()...)

	return tables
}

// pgCopyStackTables holds the COPY columns of the namespace and stack
// tables, in foreign-key order.
func pgCopyStackTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "namespaces",
			columns: []string{
				pgColName, "home_region", "failover_version", pgColCreatedAtMs,
				"allow_forced_writes", "allow_unlocked_writes", "ip_allowlist",
			},
			orderBy: []string{pgColName},
		},
		{
			table: "stacks",
			columns: []string{
				pgColNamespace, "project", pgColName, "id", pgColVersion, pgColCreatedAtMs, pgColUpdatedAtMs,
				pgColDeletedAtMs, pgColDeletedName,
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
				pgColStackID, "id", pgColKind, "dry_run", pgColStatus, pgColVersion, "revision",
				"lease_digest", "lease_expires_at_ms", pgColCreatedAtMs, pgColStartedAtMs, pgColEndedAtMs,
				pgColCreatedBy, "restored_from", "journal_version", "journal_closed", "token_id",
				"journal_records_changed",
			},
			orderBy: []string{pgColStackID, "id"},
		},
		{
			table: "checkpoints",
			columns: []string{
				pgColStackID, pgColUpdateID, "lease_digest", "schema_version", pgColSequence,
				pgColDigest, "invalid", "features", "saved_at_ms",
				"blob_key", pgColSize, "blob_size", "blob_md5",
			},
			orderBy: []string{pgColStackID, pgColUpdateID},
		},
		{
			table:   "update_events",
			columns: []string{pgColStackID, pgColUpdateID, pgColSequence, "timestamp_ms", "payload"},
			orderBy: []string{pgColStackID, pgColUpdateID, pgColSequence},
		},
		{
			table: "update_journal",
			columns: []string{
				pgColStackID, pgColUpdateID, pgColArrival, "blob_key", "sha256", "md5", pgColSize,
				"lowest_sequence", "sequence_ids", "operation_ids",
			},
			orderBy: []string{pgColStackID, pgColUpdateID, pgColArrival},
		},
		{
			table:   "update_journal_sequences",
			columns: []string{pgColStackID, pgColUpdateID, pgColSequence, pgColArrival, "sha256"},
			orderBy: []string{pgColStackID, pgColUpdateID, pgColSequence},
		},
		{
			table: "update_journal_refusals",
			columns: []string{
				pgColStackID, pgColUpdateID, pgColArrival, "sequence_ids", "status", "reason",
			},
			orderBy: []string{pgColStackID, pgColUpdateID, pgColArrival},
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
				pgColNamespace, pgColTool, pgColName, pgColUnitID, pgColDisplayName,
				"last_changed_ms", pgColStatus, "resource_count",
				"lock_holder", "lock_since_ms", "lock_operation",
			},
			orderBy: []string{pgColNamespace, pgColTool, pgColName},
		},
		{
			table:   "inventory_deleted",
			columns: []string{pgColNamespace, pgColTool, pgColUnitID, pgColName, pgColDeletedAtMs, "purge_after_ms"},
			orderBy: []string{pgColTool, pgColUnitID},
		},
		{
			table: "collections",
			columns: []string{
				"id", pgColNamespace, pgColNameKey, pgColName, pgColCreatedAtMs,
			},
			orderBy: []string{pgColNamespace, pgColNameKey},
		},
		{
			table: "collection_members",
			columns: []string{
				pgColUnitID, pgColNamespace, "collection_key", "assigned_at_ms",
			},
			orderBy: []string{pgColUnitID},
		},
		{
			table:   "blob_purges",
			columns: []string{pgColTool, pgColUnitID, pgColNamespace, "purged_at_ms"},
			orderBy: []string{pgColTool, pgColUnitID},
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
				"lock_reason", "locked_at_ms", "current_state_version_id", pgColCreatedAtMs, pgColUpdatedAtMs,
				"allow_forced_writes", "allow_unlocked_writes", pgColDeletedAtMs, pgColDeletedName,
			},
			orderBy: []string{"id"},
		},
		{
			table: "state_versions",
			columns: []string{
				"id", "workspace_id", pgColStatus, "serial", "lineage", "md5", pgColSize,
				"content_key", "outputs", "force", "upload_digest", "upload_expires_at_ms",
				"holder", "lock_id", pgColCreatedAtMs, "finalized_at_ms", "restored_from", "sealed",
				"sensitive_marks", "fenced_by", "unlocked_write",
			},
			orderBy: []string{"id"},
		},
		{
			table: "audit_entries",
			columns: []string{
				pgColNamespace, pgColSequence, pgColSubject, "idp_subject", "token_id", "action",
				"object", pgColVersion, "value_path", "at_ms", "source_ip", "user_agent",
				"channel", pgColKind, "outcome", "request_id", "prev_hash", "hash", "value_count",
				"changes", "object_id", "fence_version",
			},
			orderBy: []string{pgColNamespace, pgColSequence},
		},
		{
			table:   "audit_sink_cursors",
			columns: []string{pgColSink, pgColNamespace, pgColSequence, pgColUpdatedAtMs},
			orderBy: []string{pgColSink, pgColNamespace},
		},
		{
			table:   "audit_sink_leases",
			columns: []string{pgColSink, "holder", pgColExpiresAtMs},
			orderBy: []string{pgColSink},
		},
		{
			table:   "backups",
			columns: []string{"id", pgColState, pgColStartedAtMs, pgColEndedAtMs, "target", "error"},
			orderBy: []string{"id"},
		},
		{
			table: "restores",
			columns: []string{
				"id", pgColState, "backup_id", "pre_backup_id", "requested_by",
				pgColStartedAtMs, pgColEndedAtMs, "error",
			},
			orderBy: []string{pgColStartedAtMs, "id"},
		},
		{
			table:   "installation",
			columns: []string{"id", pgColState},
			orderBy: []string{"id"},
		},
	}
}

// pgCopySessionTables holds the COPY columns of the session and sign-in
// request tables.
func pgCopySessionTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "sessions",
			columns: []string{
				"id", pgColPrincipalID, pgColDigest, "provider", pgColCreatedAtMs, "last_seen_at_ms", "auth_time_ms",
				"ended_at_ms", "end_reason", "source_ip", "user_agent",
			},
			orderBy: []string{"id"},
		},
		{
			table: "sign_in_requests",
			columns: []string{
				pgColDigest, "provider", "nonce", "verifier", "return_path", pgColCreatedAtMs, pgColExpiresAtMs,
				"max_age_ms",
			},
			orderBy: []string{pgColDigest},
		},
		{
			table:   "oidc_providers",
			columns: []string{pgColProviderID, "issuer", pgColCreatedAtMs},
			orderBy: []string{pgColProviderID},
		},
		{
			table:   "installation_secret_keys",
			columns: []string{"scope_id", "wrapper_id", "ciphertext", pgColCreatedAtMs},
			orderBy: []string{"scope_id"},
		},
		{
			table: "sign_in_providers",
			columns: []string{
				pgColProviderID, "issuer", pgColDisplayName, "client_id", "client_secret", "scopes", "auto_create",
				"enabled", pgColCreatedAtMs, pgColUpdatedAtMs, "version", "mode", "authorization_url", "token_url",
				"userinfo_url", "subject_claim", "name_claim", "username_claim", "email_claim",
			},
			orderBy: []string{pgColProviderID},
		},
	}
}

// pgCopyPrincipalsTable holds the COPY columns of the principals table.
func pgCopyPrincipalsTable() pgCopyTable {
	return pgCopyTable{
		table: backup.TablePrincipals,
		columns: []string{
			"id", pgColSubject, "display_name", pgColKind, pgColSource, pgColCreatedAtMs, pgColUpdatedAtMs,
			pgColDeletedAtMs, "disabled_at_ms", "last_active_at_ms", "last_sign_in_at_ms", "namespace",
			"break_glass",
		},
		orderBy: []string{"id"},
	}
}

// pgCopyIdentityTables holds the COPY columns of the principal, group,
// role and grant tables (ADR 52).
func pgCopyIdentityTables() []pgCopyTable {
	return append(pgCopyPrincipalTables(), pgCopyGrantTables()...)
}

// pgCopyPrincipalTables holds the COPY columns of the principal, group,
// member and token tables.
func pgCopyPrincipalTables() []pgCopyTable {
	return []pgCopyTable{
		pgCopyPrincipalsTable(),
		{
			table: backup.TablePrincipalGroups,
			columns: []string{
				"id", pgColName, "name_key", "description", pgColSource, pgColCreatedAtMs, pgColUpdatedAtMs,
				pgColDeletedAtMs,
			},
			orderBy: []string{"id"},
		},
		{
			table: backup.TableGroupMembers,
			columns: []string{
				"group_id", pgColPrincipalID, "added_at_ms", "removed_at_ms", pgColSource, "source_id",
				pgColState, "suspended_at_ms", "pinned",
			},
			orderBy: []string{"group_id", pgColPrincipalID, "added_at_ms"},
		},
		{
			table: backup.TableAccessTokens,
			columns: []string{
				"id",
				pgColPrincipalID,
				pgColName,
				pgColKind,
				pgColDigest,
				pgColCreatedAtMs,
				pgColExpiresAtMs,
				"last_used_at_ms",
				"revoked_at_ms",
				"replaced_by",
				pgColCreatedBy,
				"disabled_at_ms",
			},
			orderBy: []string{"id"},
		},
	}
}

// pgCopyGrantTables holds the COPY columns of the role, grant and policy
// tables.
func pgCopyGrantTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: backup.TableRoles,
			columns: []string{
				"id", pgColName, "name_key", "description", "actions", pgColCreatedAtMs, pgColUpdatedAtMs,
				pgColDeletedAtMs,
			},
			orderBy: []string{"id"},
		},
		{
			table: backup.TableGrants,
			columns: []string{
				"id", pgColSubject, "subject_kind", "subject_id", "role", "object", "target", "object_kind",
				pgColNamespace, "condition", pgColCreatedAtMs, pgColCreatedBy, "revoked_at_ms", "revoked_by",
			},
			orderBy: []string{"id"},
		},
		{
			table: "access_token_policy",
			columns: []string{
				"id", "max_lifetime_ms", "inactivity_ms", "disable_tokens", "disable_accounts",
				"disable_break_glass", pgColUpdatedAtMs,
			},
			orderBy: []string{"id"},
		},
		{
			table:   "session_policy",
			columns: []string{"id", "idle_ms", "max_length_ms", pgColUpdatedAtMs, "token_freshness_ms"},
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

	return pgCopyTable{}, fmt.Errorf("backup table %q has no COPY column list: %w", table, backup.ErrCoverage)
}

// CheckCoverage implements backup.Coverage: it compares the COPY column list of
// each table of backup.Tables with the columns that the current schema holds for
// the table (ADR 42, decision 5). A table without a list, or a column that
// differs, fails with backup.ErrCoverage.
func (s *Store) CheckCoverage(ctx context.Context) error {
	for _, table := range backup.Tables {
		spec, err := pgCopySpec(table)
		if err != nil {
			return err
		}

		live, liveErr := s.liveColumns(ctx, table)
		if liveErr != nil {
			return liveErr
		}

		if checkErr := backup.CheckColumns(table, spec.columns, live); checkErr != nil {
			return checkErr
		}
	}

	return s.checkPortableColumns(ctx)
}

// checkPortableColumns compares the portable spec with the live columns of each
// table. The PostgreSQL names of the portable columns are the portable names
// (ADR 42, decision 3), and the export writes the spec, so the spec must match the
// schema.
func (s *Store) checkPortableColumns(ctx context.Context) error {
	for _, spec := range backup.PortableTables() {
		listed := make([]string, 0, len(spec.Columns))
		for _, column := range spec.Columns {
			listed = append(listed, column.Name)
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

// liveColumns returns the column names of table in the current schema, in
// ordinal order.
func (s *Store) liveColumns(ctx context.Context, table string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT column_name FROM information_schema.columns`+
			` WHERE table_schema = current_schema() AND table_name = $1 ORDER BY ordinal_position`,
		table,
	)
	if err != nil {
		return nil, fmt.Errorf("read columns of table %q: %w", table, backup.ErrSchemaRead)
	}

	defer rows.Close()

	var columns []string

	for rows.Next() {
		var column string

		if scanErr := rows.Scan(&column); scanErr != nil {
			return nil, fmt.Errorf("scan column of table %q: %w", table, backup.ErrSchemaRead)
		}

		columns = append(columns, column)
	}

	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("read columns of table %q: %w", table, backup.ErrSchemaRead)
	}

	return columns, nil
}

// pgIdent quotes one identifier. Table and column names come from the
// constant spec above, never from the network.
func pgIdent(name string) string {
	return `"` + name + `"`
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
// column always holds 16 bytes; anything else is corrupt data. Size and MD5
// stay those of the plain bytes; sealed versions mark their references
// sealed, so the backup opens them to check and stores the sealed bytes.
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

	return backup.BlobRef{MD5: md5, Key: row.ContentKey, Size: row.Size, Sealed: row.Sealed}, nil
}

// stagingSchemaPrefix prefixes every staging schema name. The prefix check in
// dropStagingSchema keeps a programming error from dropping a live schema.
const stagingSchemaPrefix = "restore_stage_"

// stagingInput groups what buildStaging needs: the staging schema, the step that
// loads its tables, and the manifest summary.
type stagingInput struct {
	load    func(ctx context.Context, schema string) error
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

	if loadErr := in.load(ctx, in.staging); loadErr != nil {
		return backup.Summary{}, loadErr
	}

	if sealedErr := checkStagedSealedState(ctx, s.pool, in.staging); sealedErr != nil {
		return backup.Summary{}, sealedErr
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

	wrappers, err := stagingWrapperIDs(ctx, s.pool, staging)
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

// DiscardStaging implements backup.CheckStore: it drops the staging schema, if
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

			if err := recordMigration(ctx, tx, m); err != nil {
				return fmt.Errorf("record staging migration: %w", err)
			}
		}

		return nil
	})
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

// checkStagedSealedState refuses a staged backup with a state version that
// is not sealed: every stored version is sealed, so an import with a plain
// row fails instead of serving plain state.
func checkStagedSealedState(ctx context.Context, pool *pgxpool.Pool, staging string) error {
	var unsealed bool

	query := "SELECT EXISTS(SELECT 1 FROM " + stagingTable(staging, "state_versions") + " WHERE NOT sealed)"
	if err := pool.QueryRow(ctx, query).Scan(&unsealed); err != nil {
		return fmt.Errorf("read staged state versions: %w", backup.ErrExportRead)
	}

	if unsealed {
		return fmt.Errorf("import holds an unsealed state version: %w", workspace.ErrUnsealedState)
	}

	return nil
}

// stagingWrapperIDs returns the distinct wrapper IDs of the namespace keys and
// of the installation keys in the staging schema, ordered, never nil.
func stagingWrapperIDs(ctx context.Context, pool *pgxpool.Pool, staging string) ([]string, error) {
	keys, err := stagingStrings(ctx, pool, staging, "secret_keys", "wrapper_id")
	if err != nil {
		return nil, err
	}

	installation, err := stagingStrings(ctx, pool, staging, "installation_secret_keys", "wrapper_id")
	if err != nil {
		return nil, err
	}

	out := slices.Concat(keys, installation)
	slices.Sort(out)

	return slices.Compact(out), nil
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

// stagingBlobRefs reads the blob references of the staging schema: every blob
// key the staged state versions, the staged checkpoints and the staged journal
// batches name, with the size and MD5 of the plain bytes, ordered by key.
// Sealed state versions mark their references sealed; checkpoint and journal
// blobs are never sealed. A pending version names no content, so only
// finalized versions appear. It matches BackupBlobs, so the staged summary
// compares equal with the manifest.
func stagingBlobRefs(ctx context.Context, pool *pgxpool.Pool, staging string) ([]backup.BlobRef, error) {
	rows, err := pool.Query(
		ctx,
		"SELECT content_key, size, md5, sealed FROM "+stagingTable(staging, "state_versions")+
			" WHERE content_key <> '' UNION ALL SELECT blob_key AS content_key, blob_size AS size, "+
			"blob_md5 AS md5, FALSE AS sealed FROM "+stagingTable(staging, "checkpoints")+
			" UNION ALL SELECT blob_key AS content_key, size AS size, "+
			"md5 AS md5, FALSE AS sealed FROM "+stagingTable(staging, "update_journal")+
			" ORDER BY content_key",
	)
	if err != nil {
		return nil, fmt.Errorf("read staged blob references: %w", err)
	}

	defer rows.Close()

	out := []backup.BlobRef{}

	for rows.Next() {
		var (
			key    string
			size   int64
			raw    []byte
			sealed bool
		)

		if err := rows.Scan(&key, &size, &raw, &sealed); err != nil {
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
		out = append(out, backup.BlobRef{MD5: sum, Key: key, Size: size, Sealed: sealed})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read staged blob references: %w", err)
	}

	return out, nil
}

// checkStagedBlobRefs compares the blob references of the staged data with the
// manifest: the staged data must name exactly the keys of the manifest with
// the same sealed flags. Unsealed entries also match in size and checksum;
// sealed entries name sealed stored bytes in the manifest, so only the key
// and the flag match.
func checkStagedBlobRefs(staged, want []backup.BlobRef) error {
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

// schemaVersion returns the highest applied migration version.
func (s *Store) schemaVersion(ctx context.Context) (int, error) {
	var version int64

	const selectVersion = `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`

	if err := s.pool.QueryRow(ctx, selectVersion).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}

	return int(version), nil
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
		return s.createBackupConflict(ctx, r.ID)
	}

	return nil
}

// FinishBackup implements backup.Registry: it moves the running backup id to
// a finished state with the error text and end time, and returns the
// finished row. Any state but succeeded or failed reports ErrNotRunning, as
// do a missing row (ErrNotFound) and a row that is not running.
//
//nolint:dupl // The backup and restore registries mirror by design: separate tables with distinct domain types.
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
// (schema_migrations, and audit_chain_state, the chain state of this
// installation) is not data and stays out.
func (s *Store) SchemaTables(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT table_name FROM information_schema.tables`+
		` WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`+
		` AND table_name NOT IN ('schema_migrations', 'audit_chain_state') ORDER BY table_name`)
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

// createBackupConflict answers why CreateBackup inserted no row: the ID is
// taken (ErrAlreadyExists), or the unique index on running backups refused a
// second running backup (ErrAlreadyRunning).
func (s *Store) createBackupConflict(ctx context.Context, id backup.ID) error {
	_, err := s.GetBackup(ctx, id)
	if err == nil {
		return backup.ErrAlreadyExists
	}

	if errors.Is(err, backup.ErrNotFound) {
		return backup.ErrAlreadyRunning
	}

	return err
}

// createRestoreConflict answers why CreateRestore inserted no row: the ID is
// taken (ErrRestoreExists), or the unique index on running restores refused a
// second running restore (ErrRestoreRunning).
func (s *Store) createRestoreConflict(ctx context.Context, id backup.RestoreID) error {
	_, err := s.GetRestore(ctx, id)
	if err == nil {
		return backup.ErrRestoreExists
	}

	if errors.Is(err, backup.ErrRestoreNotFound) {
		return backup.ErrRestoreRunning
	}

	return err
}
