package backup

// Column names that more than one table holds. goconst keeps each one a
// constant.
const (
	portColName         = "name"
	portColNameKey      = "name_key"
	portColCreatedAtMs  = "created_at_ms"
	portColCreatedBy    = "created_by"
	portColNamespace    = "namespace"
	portColVersion      = "version"
	portColStackID      = "stack_id"
	portColStatus       = "status"
	portColStartedAtMs  = "started_at_ms"
	portColEndedAtMs    = "ended_at_ms"
	portColUpdateID     = "update_id"
	portColSequence     = "sequence"
	portColSize         = "size"
	portColArrival      = "arrival"
	portColState        = "state"
	portColUpdatedAtMs  = "updated_at_ms"
	portColDeletedAtMs  = "deleted_at_ms"
	portColDisabledAtMs = "disabled_at_ms"
	portColSource       = "source"
	portColKind         = "kind"
	portColSubject      = "subject"
	portColDeletedName  = "deleted_name"
	portColTool         = "tool"
	portColUnitID       = "unit_id"
	portColPrincipalID  = "principal_id"
	portColDigest       = "digest"
)

// portableSpec is the portable column list of each table of Tables (ADR 42,
// decisions 2 and 3). Each entry names the columns in the order of the schema,
// the kind of each value, and the key that orders the rows of the table file.
// A table file holds one JSON object for each row, with one member for each
// column. The names are the same in both dialects; the one column that the
// dialects name differently is mapped in each adapter.
//
//nolint:funlen,maintidx // A constant table of columns; its length is the schema.
func portableSpec() []PortableTable {
	return []PortableTable{
		{
			Table:   "namespaces",
			OrderBy: []string{portColName},
			Columns: []Column{
				{Name: portColName, Kind: KindText},
				{Name: "home_region", Kind: KindText},
				{Name: "failover_version", Kind: KindInteger},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: "allow_forced_writes", Kind: KindBool},
				{Name: "allow_unlocked_writes", Kind: KindBool},
				{Name: "ip_allowlist", Kind: KindText},
			},
		},
		{
			Table:   "stacks",
			OrderBy: []string{portColNamespace, "project", portColName},
			Columns: []Column{
				{Name: portColNamespace, Kind: KindText},
				{Name: "project", Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: portColVersion, Kind: KindInteger},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColUpdatedAtMs, Kind: KindTime},
				{Name: "id", Kind: KindText},
				{Name: portColDeletedAtMs, Kind: KindTime},
				{Name: portColDeletedName, Kind: KindText},
			},
		},
		{
			Table:   "stack_tags",
			OrderBy: []string{portColStackID, portColName},
			Columns: []Column{
				{Name: portColStackID, Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: "value", Kind: KindText},
			},
		},
		{
			Table:   "updates",
			OrderBy: []string{portColStackID, "id"},
			Columns: []Column{
				{Name: portColStackID, Kind: KindText},
				{Name: "id", Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: "dry_run", Kind: KindBool},
				{Name: portColStatus, Kind: KindText},
				{Name: portColVersion, Kind: KindInteger},
				{Name: "revision", Kind: KindInteger},
				{Name: "lease_digest", Kind: KindBytes},
				{Name: "lease_expires_at_ms", Kind: KindTime},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColStartedAtMs, Kind: KindTime},
				{Name: portColEndedAtMs, Kind: KindTime},
				{Name: portColCreatedBy, Kind: KindText},
				{Name: "restored_from", Kind: KindInteger},
				{Name: "journal_version", Kind: KindInteger},
				{Name: "journal_closed", Kind: KindBool},
				{Name: "token_id", Kind: KindText},
			},
		},
		{
			Table:   "checkpoints",
			OrderBy: []string{portColStackID, portColUpdateID},
			Columns: []Column{
				{Name: portColStackID, Kind: KindText},
				{Name: portColUpdateID, Kind: KindText},
				{Name: "lease_digest", Kind: KindBytes},
				{Name: "schema_version", Kind: KindInteger},
				{Name: portColSequence, Kind: KindInteger},
				{Name: portColDigest, Kind: KindBytes},
				{Name: "invalid", Kind: KindBool},
				{Name: "features", Kind: KindJSON},
				{Name: "deployment", Kind: KindBytes},
				{Name: "saved_at_ms", Kind: KindTime},
				{Name: "resource_count", Kind: KindInteger},
				{Name: "blob_key", Kind: KindText},
				{Name: portColSize, Kind: KindInteger},
				{Name: "blob_size", Kind: KindInteger},
				{Name: "blob_md5", Kind: KindBytes},
			},
		},
		{
			Table:   "update_events",
			OrderBy: []string{portColStackID, portColUpdateID, portColSequence},
			Columns: []Column{
				{Name: portColStackID, Kind: KindText},
				{Name: portColUpdateID, Kind: KindText},
				{Name: portColSequence, Kind: KindInteger},
				{Name: "timestamp_ms", Kind: KindTime},
				{Name: "payload", Kind: KindBytes},
			},
		},
		{
			Table:   "update_journal",
			OrderBy: []string{portColStackID, portColUpdateID, portColArrival},
			Columns: []Column{
				{Name: portColStackID, Kind: KindText},
				{Name: portColUpdateID, Kind: KindText},
				{Name: portColArrival, Kind: KindInteger},
				{Name: "blob_key", Kind: KindText},
				{Name: "sha256", Kind: KindBytes},
				{Name: "md5", Kind: KindBytes},
				{Name: portColSize, Kind: KindInteger},
				{Name: "lowest_sequence", Kind: KindInteger},
				{Name: "sequence_ids", Kind: KindText},
				{Name: "operation_ids", Kind: KindText},
			},
		},
		{
			Table:   "update_journal_sequences",
			OrderBy: []string{portColStackID, portColUpdateID, portColSequence},
			Columns: []Column{
				{Name: portColStackID, Kind: KindText},
				{Name: portColUpdateID, Kind: KindText},
				{Name: portColSequence, Kind: KindInteger},
				{Name: portColArrival, Kind: KindInteger},
				{Name: "sha256", Kind: KindBytes},
			},
		},
		{
			Table:   "secret_keys",
			OrderBy: []string{portColNamespace, portColStackID},
			Columns: []Column{
				{Name: portColNamespace, Kind: KindText},
				{Name: portColStackID, Kind: KindText},
				{Name: "wrapper_id", Kind: KindText},
				{Name: "ciphertext", Kind: KindBytes},
				{Name: portColCreatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "inventory",
			OrderBy: []string{portColNamespace, portColTool, portColName},
			Columns: []Column{
				{Name: portColNamespace, Kind: KindText},
				{Name: portColTool, Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: portColUnitID, Kind: KindText},
				{Name: "display_name", Kind: KindText},
				{Name: "last_changed_ms", Kind: KindTime},
				{Name: portColStatus, Kind: KindText},
				{Name: "resource_count", Kind: KindInteger},
				{Name: "lock_holder", Kind: KindText},
				{Name: "lock_since_ms", Kind: KindTime},
				{Name: "lock_operation", Kind: KindText},
			},
		},
		{
			Table:   "inventory_deleted",
			OrderBy: []string{portColTool, portColUnitID},
			Columns: []Column{
				{Name: portColNamespace, Kind: KindText},
				{Name: portColTool, Kind: KindText},
				{Name: portColUnitID, Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: portColDeletedAtMs, Kind: KindTime},
				{Name: "purge_after_ms", Kind: KindTime},
			},
		},
		{
			Table:   "collections",
			OrderBy: []string{portColNamespace, portColNameKey},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespace, Kind: KindText},
				{Name: portColNameKey, Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "collection_members",
			OrderBy: []string{portColUnitID},
			Columns: []Column{
				{Name: portColUnitID, Kind: KindText},
				{Name: portColNamespace, Kind: KindText},
				{Name: "collection_key", Kind: KindText},
				{Name: "assigned_at_ms", Kind: KindTime},
			},
		},
		{
			Table:   "blob_purges",
			OrderBy: []string{portColTool, portColUnitID},
			Columns: []Column{
				{Name: portColTool, Kind: KindText},
				{Name: portColUnitID, Kind: KindText},
				{Name: portColNamespace, Kind: KindText},
				{Name: "purged_at_ms", Kind: KindTime},
			},
		},
		{
			Table:   "workspaces",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespace, Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: "revision", Kind: KindInteger},
				{Name: "lock_id", Kind: KindText},
				{Name: "lock_holder", Kind: KindText},
				{Name: "lock_reason", Kind: KindText},
				{Name: "locked_at_ms", Kind: KindTime},
				{Name: "current_state_version_id", Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColUpdatedAtMs, Kind: KindTime},
				{Name: "allow_forced_writes", Kind: KindBool},
				{Name: "allow_unlocked_writes", Kind: KindBool},
				{Name: portColDeletedAtMs, Kind: KindTime},
				{Name: portColDeletedName, Kind: KindText},
			},
		},
		{
			Table:   "state_versions",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: "workspace_id", Kind: KindText},
				{Name: portColStatus, Kind: KindText},
				{Name: "serial", Kind: KindInteger},
				{Name: "lineage", Kind: KindText},
				{Name: "md5", Kind: KindBytes},
				{Name: portColSize, Kind: KindInteger},
				{Name: "content_key", Kind: KindText},
				{Name: "outputs", Kind: KindBytes},
				{Name: "force", Kind: KindBool},
				{Name: "upload_digest", Kind: KindBytes},
				{Name: "upload_expires_at_ms", Kind: KindTime},
				{Name: "holder", Kind: KindText},
				{Name: "lock_id", Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: "finalized_at_ms", Kind: KindTime},
				{Name: "restored_from", Kind: KindText},
				{Name: "sealed", Kind: KindBool},
				{Name: "sensitive_marks", Kind: KindBytes},
				{Name: "fenced_by", Kind: KindText},
				{Name: "unlocked_write", Kind: KindBool},
			},
		},
		{
			Table:   "audit_entries",
			OrderBy: []string{portColNamespace, portColSequence},
			Columns: []Column{
				{Name: portColNamespace, Kind: KindText},
				{Name: portColSequence, Kind: KindInteger},
				{Name: portColSubject, Kind: KindText},
				{Name: "idp_subject", Kind: KindText},
				{Name: "token_id", Kind: KindText},
				{Name: "action", Kind: KindText},
				{Name: "object", Kind: KindText},
				{Name: portColVersion, Kind: KindText},
				{Name: "value_path", Kind: KindText},
				{Name: "at_ms", Kind: KindTime},
				{Name: "source_ip", Kind: KindText},
				{Name: "user_agent", Kind: KindText},
				{Name: "channel", Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: "outcome", Kind: KindText},
				{Name: "request_id", Kind: KindText},
				{Name: "prev_hash", Kind: KindText},
				{Name: "hash", Kind: KindText},
				{Name: "value_count", Kind: KindInteger},
				{Name: "changes", Kind: KindText},
				{Name: "object_id", Kind: KindText},
				{Name: "fence_version", Kind: KindInteger},
			},
		},
		{
			Table:   "principals",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColSubject, Kind: KindText},
				{Name: "display_name", Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: portColSource, Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColUpdatedAtMs, Kind: KindTime},
				{Name: portColDeletedAtMs, Kind: KindTime},
				{Name: portColDisabledAtMs, Kind: KindTime},
				{Name: "last_active_at_ms", Kind: KindTime},
				{Name: "last_sign_in_at_ms", Kind: KindTime},
			},
		},
		{
			Table:   "principal_groups",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: portColNameKey, Kind: KindText},
				{Name: "description", Kind: KindText},
				{Name: portColSource, Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColUpdatedAtMs, Kind: KindTime},
				{Name: portColDeletedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "group_members",
			OrderBy: []string{"group_id", portColPrincipalID, "added_at_ms"},
			Columns: []Column{
				{Name: "group_id", Kind: KindText},
				{Name: portColPrincipalID, Kind: KindText},
				{Name: "added_at_ms", Kind: KindTime},
				{Name: "removed_at_ms", Kind: KindTime},
			},
		},
		{
			Table:   "access_tokens",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColPrincipalID, Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: portColDigest, Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: "expires_at_ms", Kind: KindTime},
				{Name: "last_used_at_ms", Kind: KindTime},
				{Name: "revoked_at_ms", Kind: KindTime},
				{Name: "replaced_by", Kind: KindText},
				{Name: portColCreatedBy, Kind: KindText},
				{Name: portColDisabledAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "sessions",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColPrincipalID, Kind: KindText},
				{Name: portColDigest, Kind: KindText},
				{Name: "provider", Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: "last_seen_at_ms", Kind: KindTime},
				{Name: "auth_time_ms", Kind: KindTime},
				{Name: portColEndedAtMs, Kind: KindTime},
				{Name: "end_reason", Kind: KindText},
				{Name: "source_ip", Kind: KindText},
				{Name: "user_agent", Kind: KindText},
			},
		},
		{
			Table:   "sign_in_requests",
			OrderBy: []string{portColDigest},
			Columns: []Column{
				{Name: portColDigest, Kind: KindText},
				{Name: "provider", Kind: KindText},
				{Name: "nonce", Kind: KindText},
				{Name: "verifier", Kind: KindText},
				{Name: "return_path", Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: "expires_at_ms", Kind: KindTime},
			},
		},
		{
			Table:   "roles",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: "name_key", Kind: KindText},
				{Name: "description", Kind: KindText},
				{Name: "actions", Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColUpdatedAtMs, Kind: KindTime},
				{Name: portColDeletedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "grants",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColSubject, Kind: KindText},
				{Name: "subject_kind", Kind: KindText},
				{Name: "subject_id", Kind: KindText},
				{Name: "role", Kind: KindText},
				{Name: "object", Kind: KindText},
				{Name: "target", Kind: KindText},
				{Name: "object_kind", Kind: KindText},
				{Name: portColNamespace, Kind: KindText},
				{Name: "condition", Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColCreatedBy, Kind: KindText},
				{Name: "revoked_at_ms", Kind: KindTime},
				{Name: "revoked_by", Kind: KindText},
			},
		},
		{
			Table:   "audit_sink_cursors",
			OrderBy: []string{"sink", portColNamespace},
			Columns: []Column{
				{Name: "sink", Kind: KindText},
				{Name: portColNamespace, Kind: KindText},
				{Name: portColSequence, Kind: KindInteger},
				{Name: portColUpdatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "access_token_policy",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindInteger},
				{Name: "max_lifetime_ms", Kind: KindInteger},
				{Name: "inactivity_ms", Kind: KindInteger},
				{Name: "disable_tokens", Kind: KindBool},
				{Name: "disable_accounts", Kind: KindBool},
				{Name: "disable_break_glass", Kind: KindBool},
				{Name: portColUpdatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "backups",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColState, Kind: KindText},
				{Name: portColStartedAtMs, Kind: KindTime},
				{Name: portColEndedAtMs, Kind: KindTime},
				{Name: "target", Kind: KindText},
				{Name: "error", Kind: KindText},
			},
		},
		{
			Table:   "restores",
			OrderBy: []string{portColStartedAtMs, "id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColState, Kind: KindText},
				{Name: "backup_id", Kind: KindText},
				{Name: "pre_backup_id", Kind: KindText},
				{Name: "requested_by", Kind: KindText},
				{Name: portColStartedAtMs, Kind: KindTime},
				{Name: portColEndedAtMs, Kind: KindTime},
				{Name: "error", Kind: KindText},
			},
		},
		{
			Table:   "installation",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindInteger},
				{Name: portColState, Kind: KindText},
			},
		},
	}
}
