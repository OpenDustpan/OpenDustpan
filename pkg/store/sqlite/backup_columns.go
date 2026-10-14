package sqlite

// Column names that more than one table of the list holds. goconst keeps
// each one a constant.
const (
	backupColCreatedAtMs = "created_at_ms"
	backupColCreatedBy   = "created_by"
	backupColEndedAtMs   = "ended_at_ms"
	backupColName        = "name"
	backupColNamespace   = "namespace"
	backupColSequence    = "sequence"
	backupColSize        = "size"
	backupColStackID     = "stack_id"
	backupColStartedAtMs = "started_at_ms"
	backupColState       = "state"
	backupColStatus      = "status"
	backupColUpdateID    = "update_id"
	backupColVersion     = "version"
	backupColUpdatedAtMs = "updated_at_ms"
	backupColDeletedAtMs = "deleted_at_ms"
	backupColSource      = "source"
	backupColKind        = "kind"
	backupColSubject     = "subject"
	backupColDeletedName = "deleted_name"
	backupColTool        = "tool"
	backupColUnitID      = "unit_id"
)

// sqliteBackupTable is the column list of one backup table: the columns of the
// table in the SQLite schema, in schema order. The portable format names each
// column the same in both dialects (ADR 42, decision 3), so these names are the
// portable names too.
type sqliteBackupTable struct {
	table   string
	columns []string
}

// sqliteBackupTables lists the column list of every table of backup.Tables, in
// the order of backup.Tables. CheckCoverage compares each list with the live
// schema, so a migration that adds a column fails until its author adds the
// column here (ADR 42, decision 5).
//
//nolint:funlen // A constant table of column names; its length is the schema.
func sqliteBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "namespaces",
			columns: []string{
				backupColName,
				"home_region",
				"failover_version",
				backupColCreatedAtMs,
				"allow_forced_writes",
				"allow_unlocked_writes",
			},
		},
		{
			table: "stacks",
			columns: []string{
				backupColNamespace,
				"project",
				backupColName,
				backupColVersion,
				backupColCreatedAtMs,
				backupColUpdatedAtMs,
				"id",
				backupColDeletedAtMs,
				backupColDeletedName,
			},
		},
		{
			table: "stack_tags",
			columns: []string{
				backupColStackID,
				backupColName,
				"value",
			},
		},
		{
			table: "updates",
			columns: []string{
				backupColStackID,
				"id",
				backupColKind,
				"dry_run",
				backupColStatus,
				backupColVersion,
				"revision",
				"lease_digest",
				"lease_expires_at_ms",
				backupColCreatedAtMs,
				backupColStartedAtMs,
				backupColEndedAtMs,
				backupColCreatedBy,
				"restored_from",
				"journal_version",
				"journal_closed",
			},
		},
		{
			table: "checkpoints",
			columns: []string{
				backupColStackID,
				backupColUpdateID,
				"lease_digest",
				"schema_version",
				backupColSequence,
				"digest",
				"invalid",
				"features_json",
				"deployment",
				"saved_at_ms",
				"resource_count",
				"blob_key",
				backupColSize,
				"blob_size",
				"blob_md5",
			},
		},
		{
			table: "update_events",
			columns: []string{
				backupColStackID,
				backupColUpdateID,
				backupColSequence,
				"timestamp_ms",
				"payload",
			},
		},
		{
			table: "update_journal",
			columns: []string{
				backupColStackID,
				backupColUpdateID,
				"arrival",
				"blob_key",
				"sha256",
				"md5",
				backupColSize,
				"lowest_sequence",
				"sequence_ids",
				"operation_ids",
			},
		},
		{
			table: "update_journal_sequences",
			columns: []string{
				backupColStackID,
				backupColUpdateID,
				backupColSequence,
				"arrival",
				"sha256",
			},
		},
		{
			table: "secret_keys",
			columns: []string{
				backupColNamespace,
				backupColStackID,
				"wrapper_id",
				"ciphertext",
				backupColCreatedAtMs,
			},
		},
		{
			table: "inventory",
			columns: []string{
				backupColNamespace,
				backupColTool,
				backupColName,
				backupColUnitID,
				"display_name",
				"last_changed_ms",
				backupColStatus,
				"resource_count",
				"lock_holder",
				"lock_since_ms",
				"lock_operation",
			},
		},
		{
			table: "inventory_deleted",
			columns: []string{
				backupColNamespace,
				backupColTool,
				backupColUnitID,
				backupColName,
				backupColDeletedAtMs,
				"purge_after_ms",
			},
		},
		{
			table: "blob_purges",
			columns: []string{
				backupColTool,
				backupColUnitID,
				backupColNamespace,
				"purged_at_ms",
			},
		},
		{
			table: "workspaces",
			columns: []string{
				"id",
				backupColNamespace,
				backupColName,
				"revision",
				"lock_id",
				"lock_holder",
				"lock_reason",
				"locked_at_ms",
				"current_state_version_id",
				backupColCreatedAtMs,
				backupColUpdatedAtMs,
				"allow_forced_writes",
				"allow_unlocked_writes",
				backupColDeletedAtMs,
				backupColDeletedName,
			},
		},
		{
			table: "state_versions",
			columns: []string{
				"id",
				"workspace_id",
				backupColStatus,
				"serial",
				"lineage",
				"md5",
				backupColSize,
				"content_key",
				"outputs",
				"force",
				"upload_digest",
				"upload_expires_at_ms",
				"holder",
				"lock_id",
				backupColCreatedAtMs,
				"finalized_at_ms",
				"restored_from",
				"sealed",
				"sensitive_marks",
				"fenced_by",
				"unlocked_write",
			},
		},
		{
			table: "audit_entries",
			columns: []string{
				backupColNamespace,
				backupColSequence,
				backupColSubject,
				"idp_subject",
				"token_id",
				"action",
				"object",
				backupColVersion,
				"value_path",
				"at_ms",
				"source_ip",
				"user_agent",
				"channel",
				backupColKind,
				"outcome",
				"request_id",
				"prev_hash",
				"hash",
				"value_count",
				"object_id",
			},
		},
		{
			table: "principals",
			columns: []string{
				"id",
				backupColSubject,
				"display_name",
				backupColKind,
				backupColSource,
				backupColCreatedAtMs,
				backupColUpdatedAtMs,
				backupColDeletedAtMs,
			},
		},
		{
			table: "principal_groups",
			columns: []string{
				"id",
				backupColName,
				"name_key",
				"description",
				backupColSource,
				backupColCreatedAtMs,
				backupColUpdatedAtMs,
				backupColDeletedAtMs,
			},
		},
		{
			table:   "group_members",
			columns: []string{"group_id", "principal_id", "added_at_ms", "removed_at_ms"},
		},
		{
			table: "access_tokens",
			columns: []string{
				"id",
				"principal_id",
				backupColName,
				backupColKind,
				"digest",
				backupColCreatedAtMs,
				"expires_at_ms",
				"last_used_at_ms",
				"revoked_at_ms",
				"replaced_by",
				backupColCreatedBy,
			},
		},
		{
			table: "roles",
			columns: []string{
				"id",
				backupColName,
				"name_key",
				"description",
				"actions",
				backupColCreatedAtMs,
				backupColUpdatedAtMs,
				backupColDeletedAtMs,
			},
		},
		{
			table: "grants",
			columns: []string{
				"id",
				backupColSubject,
				"subject_kind",
				"subject_id",
				"role",
				"object",
				"target",
				"object_kind",
				backupColNamespace,
				"condition",
				backupColCreatedAtMs,
				backupColCreatedBy,
				"revoked_at_ms",
				"revoked_by",
			},
		},
		{
			table: "backups",
			columns: []string{
				"id",
				backupColState,
				backupColStartedAtMs,
				backupColEndedAtMs,
				"target",
				"error",
			},
		},
		{
			table: "restores",
			columns: []string{
				"id",
				backupColState,
				"backup_id",
				"pre_backup_id",
				"requested_by",
				backupColStartedAtMs,
				backupColEndedAtMs,
				"error",
			},
		},
		{
			table: "installation",
			columns: []string{
				"id",
				backupColState,
			},
		},
	}
}
