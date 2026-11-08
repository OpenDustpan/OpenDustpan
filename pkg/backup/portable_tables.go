package backup

import "slices"

// Column names that more than one table holds. goconst keeps each one a
// constant.
const (
	portColName          = "name"
	portColIssuer        = "issuer"
	portColNameKey       = "name_key"
	portColCreatedAtMs   = "created_at_ms"
	portColDisplayName   = "display_name"
	portColProviderID    = "provider_id"
	portColCreatedBy     = "created_by"
	portColNamespace     = "namespace"
	portColNamespaceID   = "namespace_id"
	portColReason        = "reason"
	portTableNamespaces  = "namespaces"
	portTableManualLocks = "manual_locks"
	portColVersion       = "version"
	portColStackID       = "stack_id"
	portColStatus        = "status"
	portColStartedAtMs   = "started_at_ms"
	portColEndedAtMs     = "ended_at_ms"
	portColUpdateID      = "update_id"
	portColSequence      = "sequence"
	portColSize          = "size"
	portColArrival       = "arrival"
	portColState         = "state"
	portColUpdatedAtMs   = "updated_at_ms"
	portColDeletedAtMs   = "deleted_at_ms"
	portColDisabledAtMs  = "disabled_at_ms"
	portColSource        = "source"
	portColSourceID      = "source_id"
	portColSourceKind    = "source_kind"
	portColGroupID       = "group_id"
	portColClaimValue    = "claim_value"
	portColKind          = "kind"
	portColScopeID       = "scope_id"
	portColRevision      = "revision"
	portColWrapperID     = "wrapper_id"
	portColSubject       = "subject"
	portColDeletedName   = "deleted_name"
	portColTool          = "tool"
	portColPosition      = "position"
	portColProject       = "project"
	portColUnitID        = "unit_id"
	portColPrincipalID   = "principal_id"
	portColDigest        = "digest"
	portColSink          = "sink"
	portColExpiresAtMs   = "expires_at_ms"
	portColObjectKind    = "object_kind"
	portColObjectID      = "object_id"
	portColResourceKey   = "resource_key"
	portColAtMs          = "at_ms"
	portColScopeKey      = "scope_key"
	portColSeq           = "seq"
	portColBlobKey       = "blob_key"
	portColSHA256Text    = "sha256"
	portColOwner         = "owner"
	portColJobID         = "job_id"
	portColBody          = "body"
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
	core := []PortableTable{
		{
			Table:   portTableNamespaces,
			OrderBy: []string{portColName},
			Columns: []Column{
				{Name: portColName, Kind: KindText},
				{Name: "primary_installation", Kind: KindText},
				{Name: "failover_version", Kind: KindInteger},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: "allow_forced_writes", Kind: KindBool},
				{Name: "allow_unlocked_writes", Kind: KindBool},
				{Name: "ip_allowlist", Kind: KindText},
				{Name: "id", Kind: KindText},
				{Name: portColOutputSharingDefault, Kind: KindText},
				{Name: portColSourceFilter, Kind: KindText},
			},
		},
		{
			Table:   "stacks",
			OrderBy: []string{portColNamespace, portColProject, portColName},
			Columns: []Column{
				{Name: portColNamespace, Kind: KindText},
				{Name: portColProject, Kind: KindText},
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
				{Name: portColRevision, Kind: KindInteger},
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
				{Name: "journal_records_changed", Kind: KindBool},
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
				{Name: "saved_at_ms", Kind: KindTime},
				{Name: portColBlobKey, Kind: KindText},
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
				{Name: portColBlobKey, Kind: KindText},
				{Name: portColSHA256, Kind: KindBytes},
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
				{Name: portColSHA256, Kind: KindBytes},
			},
		},
		{
			Table:   "update_journal_refusals",
			OrderBy: []string{portColStackID, portColUpdateID, portColArrival},
			Columns: []Column{
				{Name: portColStackID, Kind: KindText},
				{Name: portColUpdateID, Kind: KindText},
				{Name: portColArrival, Kind: KindInteger},
				{Name: "sequence_ids", Kind: KindText},
				{Name: "status", Kind: KindInteger},
				{Name: portColReason, Kind: KindText},
			},
		},
		{
			Table:   "secret_keys",
			OrderBy: []string{portColNamespace, portColStackID},
			Columns: []Column{
				{Name: portColNamespace, Kind: KindText},
				{Name: portColStackID, Kind: KindText},
				{Name: portColWrapperID, Kind: KindText},
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
				{Name: portColDisplayName, Kind: KindText},
				{Name: "last_changed_ms", Kind: KindTime},
				{Name: portColStatus, Kind: KindText},
				{Name: portColResourceCount, Kind: KindInteger},
				{Name: "lock_holder", Kind: KindText},
				{Name: "lock_since_ms", Kind: KindTime},
				{Name: "lock_operation", Kind: KindText},
				{Name: "last_release_lock_id", Kind: KindText},
				{Name: "last_release_holder", Kind: KindText},
				{Name: "last_release_by", Kind: KindText},
				{Name: portColLastReleaseKind, Kind: KindText},
				{Name: "last_release_at_ms", Kind: KindTime},
			},
		},
		{
			Table:   "inventory_labels",
			OrderBy: []string{portColUnitID, "label_key"},
			Columns: []Column{
				{Name: portColNamespace, Kind: KindText},
				{Name: portColUnitID, Kind: KindText},
				{Name: "label_key", Kind: KindText},
				{Name: "label_value", Kind: KindText},
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
			Table:   "inventory_change_heads",
			OrderBy: []string{portColNamespaceID},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "last_seq", Kind: KindInteger},
			},
		},
		{
			Table:   "inventory_changes",
			OrderBy: []string{portColNamespaceID, portColSeq},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColSeq, Kind: KindInteger},
				{Name: portColAtMs, Kind: KindTime},
				{Name: portColTool, Kind: KindText},
				{Name: portColUnitID, Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: portColDisplayName, Kind: KindText},
				{Name: portColStatus, Kind: KindText},
				{Name: "collection", Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: portColResourceCount, Kind: KindInteger},
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
			Table:   "placement_rules",
			OrderBy: []string{portColNamespace, portColPosition, "id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespace, Kind: KindText},
				{Name: portColPosition, Kind: KindInteger},
				{Name: portColTool, Kind: KindText},
				{Name: portColProject, Kind: KindText},
				{Name: "stack", Kind: KindText},
				{Name: "collection_key", Kind: KindText},
				{Name: "created_by", Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
			},
		},
		{
			Table: "object_context",
			OrderBy: []string{
				portColNamespaceID,
				portColObjectKind,
				portColObjectID,
				portColResourceKey,
				portColSource,
			},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColObjectKind, Kind: KindText},
				{Name: portColObjectID, Kind: KindText},
				{Name: portColTool, Kind: KindText},
				{Name: portColResourceKey, Kind: KindText},
				{Name: portColSource, Kind: KindText},
				{Name: "fields", Kind: KindText},
				{Name: "warning", Kind: KindText},
				{Name: "item_id", Kind: KindText},
				{Name: "item_seq", Kind: KindInteger},
				{Name: "resource_deleted_at_ms", Kind: KindTime},
				{Name: "marks_seq", Kind: KindInteger},
				{Name: portColUpdatedAtMs, Kind: KindTime},
				{Name: "updated_by", Kind: KindText},
			},
		},
		{
			Table: "object_context_owners",
			OrderBy: []string{
				portColNamespaceID,
				portColOwner,
				portColObjectKind,
				portColObjectID,
				portColResourceKey,
				portColSource,
			},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColOwner, Kind: KindText},
				{Name: portColObjectKind, Kind: KindText},
				{Name: portColObjectID, Kind: KindText},
				{Name: portColResourceKey, Kind: KindText},
				{Name: portColSource, Kind: KindText},
			},
		},
		{
			Table:   "gate_objects",
			OrderBy: []string{portColNamespaceID, portColScopeKey, portColKind, "id"},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColScopeKey, Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: "id", Kind: KindText},
				{Name: "level", Kind: KindText},
				{Name: portColUnitID, Kind: KindText},
				{Name: portColVersion, Kind: KindInteger},
				{Name: portColBody, Kind: KindText},
				{Name: portColUpdatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "gate_references",
			OrderBy: []string{"ref_kind", "ref_id", portColNamespaceID, portColScopeKey, "owner_kind", "owner_id"},
			Columns: []Column{
				{Name: "ref_kind", Kind: KindText},
				{Name: "ref_id", Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColScopeKey, Kind: KindText},
				{Name: "owner_kind", Kind: KindText},
				{Name: "owner_id", Kind: KindText},
			},
		},
		{
			Table:   "gate_records",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "point", Kind: KindText},
				{Name: "change_kind", Kind: KindText},
				{Name: "change_id", Kind: KindText},
				{Name: portColAtMs, Kind: KindTime},
				{Name: "last_at_ms", Kind: KindTime},
				{Name: "count", Kind: KindInteger},
				{Name: "refusal_key", Kind: KindText},
				{Name: "body", Kind: KindText},
			},
		},
		{
			Table:   "blob_purges",
			OrderBy: []string{portColTool, portColUnitID},
			Columns: []Column{
				{Name: portColTool, Kind: KindText},
				{Name: portColUnitID, Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "purged_at_ms", Kind: KindTime},
			},
		},
		{
			Table:   portTableWorkspaces,
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespace, Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: portColRevision, Kind: KindInteger},
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
				{Name: "last_release_lock_id", Kind: KindText},
				{Name: "last_release_holder", Kind: KindText},
				{Name: "last_release_by", Kind: KindText},
				{Name: portColLastReleaseKind, Kind: KindText},
				{Name: "last_release_at_ms", Kind: KindTime},
			},
		},
		{
			Table:   "state_versions",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColWorkspaceID, Kind: KindText},
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
				{Name: portColAtMs, Kind: KindTime},
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
				{Name: "principal_kind", Kind: KindText},
				{Name: portColJobID, Kind: KindText},
				{Name: portColReason, Kind: KindText},
			},
		},
		{
			Table:   TablePrincipals,
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColSubject, Kind: KindText},
				{Name: portColDisplayName, Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: portColSource, Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColUpdatedAtMs, Kind: KindTime},
				{Name: portColDeletedAtMs, Kind: KindTime},
				{Name: portColDisabledAtMs, Kind: KindTime},
				{Name: "last_active_at_ms", Kind: KindTime},
				{Name: "last_sign_in_at_ms", Kind: KindTime},
				{Name: "namespace", Kind: KindText},
				{Name: "break_glass", Kind: KindBool},
			},
		},
		{
			Table:   TablePrincipalGroups,
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
				{Name: "closed_to_identity_provider", Kind: KindBool},
			},
		},
		{
			Table:   TableGroupMembers,
			OrderBy: []string{portColGroupID, portColPrincipalID, "added_at_ms"},
			Columns: []Column{
				{Name: portColGroupID, Kind: KindText},
				{Name: portColPrincipalID, Kind: KindText},
				{Name: "added_at_ms", Kind: KindTime},
				{Name: "removed_at_ms", Kind: KindTime},
				{Name: portColSource, Kind: KindText},
				{Name: portColSourceID, Kind: KindText},
				{Name: portColState, Kind: KindText},
				{Name: "suspended_at_ms", Kind: KindTime},
				{Name: "pinned", Kind: KindBool},
			},
		},
		{
			Table:   TableGroupMappings,
			OrderBy: []string{portColSource, portColSourceID, portColClaimValue, portColGroupID},
			Columns: []Column{
				{Name: portColSource, Kind: KindText},
				{Name: portColSourceID, Kind: KindText},
				{Name: portColClaimValue, Kind: KindText},
				{Name: portColGroupID, Kind: KindText},
				{Name: portColCreatedBy, Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "member_removal_alerts",
			OrderBy: []string{portColSourceKind, portColSourceID},
			Columns: []Column{
				{Name: portColSourceKind, Kind: KindText},
				{Name: portColSourceID, Kind: KindText},
				{Name: "alerted_at_ms", Kind: KindTime},
			},
		},
		{
			Table:   "member_removals",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColSourceKind, Kind: KindText},
				{Name: portColSourceID, Kind: KindText},
				{Name: portColGroupID, Kind: KindText},
				{Name: "removed", Kind: KindInteger},
				{Name: "members_before", Kind: KindInteger},
				{Name: portColAtMs, Kind: KindTime},
			},
		},
		{
			Table:   TableTempAdmins,
			OrderBy: []string{portColPrincipalID},
			Columns: []Column{
				{Name: portColPrincipalID, Kind: KindText},
				{Name: portColSubject, Kind: KindText},
				{Name: portColExpiresAtMs, Kind: KindTime},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColCreatedBy, Kind: KindText},
				{Name: portColReason, Kind: KindText},
				{Name: "ended_at_ms", Kind: KindTime},
				{Name: "review_opened_at_ms", Kind: KindTime},
			},
		},
		{
			Table:   TableAccessTokens,
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColPrincipalID, Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: portColDigest, Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColExpiresAtMs, Kind: KindTime},
				{Name: "last_used_at_ms", Kind: KindTime},
				{Name: portColRevokedAtMs, Kind: KindTime},
				{Name: "replaced_by", Kind: KindText},
				{Name: portColCreatedBy, Kind: KindText},
				{Name: portColDisabledAtMs, Kind: KindTime},
			},
		},
		{
			Table:   TableSessions,
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
				{Name: "generation", Kind: KindInteger},
			},
		},
		{
			Table:   TableSignInRequests,
			OrderBy: []string{portColDigest},
			Columns: []Column{
				{Name: portColDigest, Kind: KindText},
				{Name: "provider", Kind: KindText},
				{Name: "nonce", Kind: KindText},
				{Name: "verifier", Kind: KindText},
				{Name: "return_path", Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColExpiresAtMs, Kind: KindTime},
				{Name: "max_age_ms", Kind: KindInteger},
			},
		},
		{
			Table:   TableOIDCProviders,
			OrderBy: []string{portColProviderID},
			Columns: []Column{
				{Name: portColProviderID, Kind: KindText},
				{Name: portColIssuer, Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   TableInstallationSecretKeys,
			OrderBy: []string{"scope_id"},
			Columns: []Column{
				{Name: portColScopeID, Kind: KindText},
				{Name: portColWrapperID, Kind: KindText},
				{Name: "ciphertext", Kind: KindBytes},
				{Name: portColCreatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   TableSignInProviders,
			OrderBy: []string{portColProviderID},
			Columns: []Column{
				{Name: portColProviderID, Kind: KindText},
				{Name: portColIssuer, Kind: KindText},
				{Name: portColDisplayName, Kind: KindText},
				{Name: "client_id", Kind: KindText},
				{Name: "client_secret", Kind: KindBytes},
				{Name: "scopes", Kind: KindText},
				{Name: "auto_create", Kind: KindBool},
				{Name: "enabled", Kind: KindBool},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColUpdatedAtMs, Kind: KindTime},
				{Name: "version", Kind: KindInteger},
				{Name: "mode", Kind: KindText},
				{Name: "authorization_url", Kind: KindText},
				{Name: "token_url", Kind: KindText},
				{Name: "userinfo_url", Kind: KindText},
				{Name: "subject_claim", Kind: KindText},
				{Name: "name_claim", Kind: KindText},
				{Name: "username_claim", Kind: KindText},
				{Name: "email_claim", Kind: KindText},
			},
		},
		{
			Table:   TableRoles,
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
			Table:   TableGrants,
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
				{Name: portColRevokedAtMs, Kind: KindTime},
				{Name: "revoked_by", Kind: KindText},
			},
		},
		{
			Table:   "audit_sink_cursors",
			OrderBy: []string{portColSink, portColNamespace},
			Columns: []Column{
				{Name: portColSink, Kind: KindText},
				{Name: portColNamespace, Kind: KindText},
				{Name: portColSequence, Kind: KindInteger},
				{Name: portColUpdatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "audit_sink_leases",
			OrderBy: []string{portColSink},
			Columns: []Column{
				{Name: portColSink, Kind: KindText},
				{Name: "holder", Kind: KindText},
				{Name: portColExpiresAtMs, Kind: KindTime},
			},
		},
		{
			Table:   TableAccessTokenPolicy,
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindInteger},
				{Name: "max_lifetime_ms", Kind: KindInteger},
				{Name: "inactivity_ms", Kind: KindInteger},
				{Name: "disable_tokens", Kind: KindBool},
				{Name: "disable_accounts", Kind: KindBool},
				{Name: "disable_break_glass", Kind: KindBool},
				{Name: portColUpdatedAtMs, Kind: KindTime},
				{Name: "exchange_lifetime_ms", Kind: KindInteger},
			},
		},
		{
			Table:   TableSessionPolicy,
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindInteger},
				{Name: "idle_ms", Kind: KindInteger},
				{Name: "max_length_ms", Kind: KindInteger},
				{Name: portColUpdatedAtMs, Kind: KindTime},
				{Name: "token_freshness_ms", Kind: KindInteger},
			},
		},
	}

	return slices.Concat(
		core,
		portableImportTables(),
		portableBlameTables(),
		portableRunnerTables(),
		portableJobTables(),
		portableJobArtifactTables(),
		portableProfileTables(),
		portableManualLockTables(),
		portablePreferenceTables(),
		portableForgeLinkTables(),
		portableLocalUserTables(),
		portableSavedViewTables(),
		portableDriftTables(),
		portableUnitSettingsTables(),
		portableGitHostTables(),
		portableRegistryTables(),
		portableSinkTables(),
		portableOutboxTables(),
		portableDeliveryTables(),
		portableTriggerTables(),
		portableItemKeyTables(),
		portableSettingTables(),
		portableSelfServiceTables(),
		portableTrustRuleTables(),
		portableBreakGlassReviewTables(),
	)
}
