package sqlite

import (
	"slices"

	"opendustpan.org/opendustpan/pkg/backup"
)

// Column names that more than one table of the list holds. goconst keeps
// each one a constant.
const (
	backupColArrival     = "arrival"
	backupColCreatedAtMs = "created_at_ms"
	backupColDisplayName = "display_name"
	backupColProviderID  = "provider_id"
	backupColCreatedBy   = "created_by"
	backupColDigest      = "digest"
	backupColPrincipalID = "principal_id"
	backupColEndedAtMs   = "ended_at_ms"
	backupColError       = "error"
	backupColName        = "name"
	backupColIssuer      = "issuer"
	backupColNameKey     = "name_key"
	backupColNamespace   = "namespace"
	backupColNamespaceID = "namespace_id"
	backupColBlobKey     = "blob_key"
	backupColReason      = "reason"
	backupColWrapperID   = "wrapper_id"
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
	backupColSourceID    = "source_id"
	backupColSourceKind  = "source_kind"
	backupColGroupID     = "group_id"
	backupColKind        = "kind"
	backupColUsedAtMs    = "used_at_ms"
	backupColSubject     = "subject"
	backupColDeletedName = "deleted_name"
	backupColTool        = "tool"
	backupColPosition    = "position"
	backupColProject     = "project"
	backupColObjectKind  = "object_kind"
	backupColObjectID    = "object_id"
	backupColAtMs        = "at_ms"
	backupColTarget      = "target"
	backupColObject      = "object"
	backupColScopeKey    = "scope_key"
	backupColUnitID      = "unit_id"
	backupColExpiresAtMs = "expires_at_ms"
	backupColSeq         = "seq"
	backupColJobID       = "job_id"
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
	base := []sqliteBackupTable{
		{
			table: "namespaces",
			columns: []string{
				backupColName,
				"primary_installation",
				"failover_version",
				backupColCreatedAtMs,
				"allow_forced_writes",
				"allow_unlocked_writes",
				"ip_allowlist",
				"id",
				"output_sharing_default",
				"source_filter",
			},
		},
		{
			table: "stacks",
			columns: []string{
				backupColNamespace,
				backupColProject,
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
				backupColRevision,
				"lease_digest",
				"lease_expires_at_ms",
				backupColCreatedAtMs,
				backupColStartedAtMs,
				backupColEndedAtMs,
				backupColCreatedBy,
				"restored_from",
				"journal_version",
				"journal_closed",
				"token_id",
				"journal_records_changed",
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
				backupColDigest,
				"invalid",
				"features_json",
				"saved_at_ms",
				backupColBlobKey,
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
				backupColArrival,
				backupColBlobKey,
				backupColSHA256,
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
				backupColArrival,
				backupColSHA256,
			},
		},
		{
			table: "update_journal_refusals",
			columns: []string{
				backupColStackID,
				backupColUpdateID,
				backupColArrival,
				"sequence_ids",
				"status",
				backupColReason,
			},
		},
		{
			table: "secret_keys",
			columns: []string{
				backupColNamespace,
				backupColStackID,
				backupColWrapperID,
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
				backupColDisplayName,
				"last_changed_ms",
				backupColStatus,
				backupColResourceCount,
				"lock_holder",
				"lock_since_ms",
				"lock_operation",
				"last_release_lock_id",
				"last_release_holder",
				"last_release_by",
				"last_release_kind",
				"last_release_at_ms",
			},
		},
		{
			table:   "inventory_labels",
			columns: []string{backupColNamespace, backupColUnitID, "label_key", "label_value"},
		},
		{
			table: "collections",
			columns: []string{
				"id", backupColNamespace, backupColNameKey, backupColName, backupColCreatedAtMs,
			},
		},
		{
			table: "collection_members",
			columns: []string{
				backupColUnitID, backupColNamespace, "collection_key", "assigned_at_ms",
			},
		},
		{
			table: "object_context",
			columns: []string{
				backupColNamespaceID,
				backupColObjectKind,
				backupColObjectID,
				backupColTool,
				"resource_key",
				backupColSource,
				"fields",
				"warning",
				"item_id",
				"item_seq",
				"resource_deleted_at_ms",
				"marks_seq",
				backupColUpdatedAtMs,
				"updated_by",
			},
		},
		{
			table: "object_context_owners",
			columns: []string{
				backupColNamespaceID, "owner", backupColObjectKind, backupColObjectID, "resource_key", backupColSource,
			},
		},
		{
			table: "gate_objects",
			columns: []string{
				backupColNamespaceID,
				backupColScopeKey,
				backupColKind,
				"id",
				"level",
				backupColUnitID,
				backupColVersion,
				backupColBody,
				backupColUpdatedAtMs,
			},
		},
		{
			table:   "gate_references",
			columns: []string{"ref_kind", "ref_id", backupColNamespaceID, backupColScopeKey, "owner_kind", "owner_id"},
		},
		{
			table: "gate_records",
			columns: []string{
				"id", backupColNamespaceID, "point", "change_kind", "change_id", backupColAtMs, "last_at_ms", "count",
				"refusal_key", backupColBody,
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
			table:   "inventory_change_heads",
			columns: []string{backupColNamespaceID, "last_seq"},
		},
		{
			table: "inventory_changes",
			columns: []string{
				backupColNamespaceID, backupColSeq, backupColAtMs, backupColTool, backupColUnitID, backupColName,
				backupColDisplayName,
				backupColStatus, "collection", backupColKind, backupColResourceCount,
			},
		},
		{
			table: "blob_purges",
			columns: []string{
				backupColTool,
				backupColUnitID,
				backupColNamespaceID,
				"purged_at_ms",
			},
		},
	}

	audit := []sqliteBackupTable{
		{
			table: "audit_entries",
			columns: []string{
				backupColNamespace,
				backupColSequence,
				backupColSubject,
				"idp_subject",
				"token_id",
				"action",
				backupColObject,
				backupColVersion,
				"value_path",
				backupColAtMs,
				"source_ip",
				"user_agent",
				"channel",
				backupColKind,
				"outcome",
				"request_id",
				"prev_hash",
				"hash",
				"value_count",
				"changes",
				"object_id",
				"fence_version",
				"principal_kind",
				backupColJobID,
				backupColReason,
			},
		},
	}

	return slices.Concat(base, sqliteWorkspaceBackupTables(), audit, sqliteLaterBackupTables())
}

// sqliteWorkspaceBackupTables lists the column lists of the workspace tables,
// in the order of backup.Tables.
func sqliteWorkspaceBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "workspaces",
			columns: []string{
				"id",
				backupColNamespace,
				backupColName,
				backupColRevision,
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
				"last_release_lock_id",
				"last_release_holder",
				"last_release_by",
				"last_release_kind",
				"last_release_at_ms",
			},
		},
		{
			table: "state_versions",
			columns: []string{
				"id",
				backupColWorkspaceID,
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
	}
}

// sqliteLaterBackupTables lists the column lists of the tables that follow the
// base list in the order of backup.Tables.
func sqliteLaterBackupTables() []sqliteBackupTable {
	return slices.Concat(
		sqliteIdentityBackupTables(),
		sqliteSessionBackupTables(),
		sqliteImportBackupTables(),
		sqliteBlameBackupTables(),
		sqliteRunnerBackupTables(),
		sqliteJobBackupTables(),
		sqliteProfileBackupTables(),
		sqliteManualLockBackupTables(),
		sqlitePreferenceBackupTables(),
		sqliteForgeLinkBackupTables(),
		sqliteLocalUserBackupTables(),
		sqliteSavedViewBackupTables(),
		sqliteDriftBackupTables(),
		sqliteUnitSettingsBackupTables(),
		sqliteGitHostBackupTables(),
		sqliteRegistryBackupTables(),
		sqliteSinkBackupTables(),
		sqliteTriggerBackupTables(),
		sqliteItemKeyBackupTables(),
		sqliteSettingBackupTables(),
		sqlitePlacementBackupTables(),
		sqliteSelfServiceBackupTables(),
		sqliteTrustRuleBackupTables(),
		sqliteBreakGlassReviewBackupTables(),
	)
}

// sqlitePlacementBackupTables lists the column list of the placement rules.
func sqlitePlacementBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "placement_rules",
			columns: []string{
				"id", backupColNamespace, backupColPosition, backupColTool, backupColProject, "stack",
				"collection_key", "created_by", backupColCreatedAtMs,
			},
		},
	}
}

// sqliteControlTables lists the column list of every table of
// backup.ControlTables: the durable tables and the installation, backups and
// restores tables that a restore keeps. CheckCoverage compares each list with
// the live schema.
func sqliteControlTables() []sqliteBackupTable {
	core := []sqliteBackupTable{
		{
			table: backup.TableDurableOperations,
			columns: []string{
				"id", "start_key", backupColKind, "input", backupColVersion, "state", "attempt", "expiries", "failures",
				"last_error", "claim_owner", "claim_token", "claim_expires_at_ms", "cancel_requested",
				"not_before_ms", "created_at_ms", "updated_at_ms",
			},
		},
		{
			table:   backup.TableDurableSteps,
			columns: []string{backupColOperationID, "name", "seq", "result", "done_at_ms"},
		},
		{
			table:   backup.TableDurableSchedules,
			columns: []string{"name", "interval_ms", "next_run_at_ms", "last_claimed_at_ms"},
		},
		{
			table:   backup.TableSignInFailures,
			columns: []string{"source", "window_end_ms", "count"},
		},
		{
			table:   backup.TableAuditChainCheck,
			columns: []string{"id", "checked_at_ms", "invalid"},
		},
		{
			table:   backup.TableAuditChainMark,
			columns: []string{"scope", "epoch", "sequence", "marked_at_ms"},
		},
		{
			table: backup.TableBackups,
			columns: []string{
				"id",
				backupColState,
				backupColStartedAtMs,
				backupColEndedAtMs,
				backupColTarget,
				"error",
			},
		},
		{
			table: backup.TableRestores,
			columns: []string{
				"id",
				backupColState,
				"backup_id",
				"pre_backup_id",
				"requested_by",
				backupColStartedAtMs,
				backupColEndedAtMs,
				"error",
				"dev_report",
				"maintenance_entered",
				"audit_events",
			},
		},
		{
			table: backup.TableInstallation,
			columns: []string{
				"id",
				backupColState,
				"kind",
			},
		},
	}

	return slices.Concat(
		core, sqliteRevocationControlTables(), sqliteRunnerControlTables(), sqliteNameLockControlTables(),
	)
}

// sqliteNameLockControlTables lists the columns of the counts of the lockout
// by name and of the key of their names.
func sqliteNameLockControlTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: backup.TableNameLocks,
			columns: []string{
				"scope", backupColName, "sub", "failures", "locked_until_ms", "level", "last_failure_ms",
				"last_lock_end_ms", "reset_after_ms",
			},
		},
		{
			table:   backup.TableNameLockKey,
			columns: []string{"id", "key"},
		},
		{
			table:   backup.TableUsedTickets,
			columns: []string{"provider_id", "ticket_digest", "until_ms"},
		},
	}
}

// sqliteRevocationControlTables lists the column list of the control table that
// holds the journal of revocations made in maintenance mode.
func sqliteRevocationControlTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: backup.TableMaintenanceRevocations,
			columns: []string{
				"id",
				backupColKind,
				backupColTarget,
				"end_reason",
				"actor",
				backupColObject,
				backupColAtMs,
			},
		},
		{
			table:   backup.TableInstanceVersions,
			columns: []string{"id", "binary_version", backupColStartedAtMs},
		},
		{
			table: backup.TableStoreChecks,
			columns: []string{
				"id", backupColTarget, backupColState, "requested_by", backupColStartedAtMs, backupColEndedAtMs,
				backupColError, "report",
			},
		},
	}
}

// sqliteEmptiedTables lists the column list of every table of
// backup.EmptiedTables. CheckCoverage compares each list with the live schema.
func sqliteEmptiedTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: backup.TableIdempotencyRecords,
			columns: []string{
				"principal", "method", "path", "idem_key", "fingerprint", "token", "status", "headers", backupColBody,
				backupColCreatedAtMs,
			},
		},
		{
			table:   backup.TableIdempotencyServerKey,
			columns: []string{"id", "wrapped_key", backupColWrapperID},
		},
		{
			table:   backup.TableDurableHeartbeats,
			columns: []string{"owner", backupColAtMs, "lease_ms"},
		},
		{
			table:   backup.TableLocalUserResets,
			columns: sqliteLocalUserResetColumns(),
		},
		{
			table:   backup.TablePasskeyChallenges,
			columns: sqlitePasskeyChallengeColumns(),
		},
		{
			table: backup.TableUnitState,
			columns: []string{
				backupColUnitID, backupColNamespaceID, "version_id", "final_version_id", "final_commit",
				"final_job", "final_at_ms",
			},
		},
	}
}

// sqliteSessionBackupTables lists the column lists of the session and
// sign-in request tables.
func sqliteSessionBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "sessions",
			columns: []string{
				"id",
				backupColPrincipalID,
				backupColDigest,
				"provider",
				backupColCreatedAtMs,
				"last_seen_at_ms",
				"auth_time_ms",
				"ended_at_ms",
				"end_reason",
				"source_ip",
				"user_agent",
				"generation",
			},
		},
		{
			table: "sign_in_requests",
			columns: []string{
				backupColDigest,
				"provider",
				"nonce",
				"verifier",
				"return_path",
				backupColCreatedAtMs,
				backupColExpiresAtMs,
				"max_age_ms",
			},
		},
		{
			table:   "oidc_providers",
			columns: []string{backupColProviderID, backupColIssuer, backupColCreatedAtMs},
		},
		{
			table:   "installation_secret_keys",
			columns: []string{backupColScopeID, backupColWrapperID, "ciphertext", backupColCreatedAtMs},
		},
		{
			table: "sign_in_providers",
			columns: []string{
				backupColProviderID,
				"issuer",
				backupColDisplayName,
				"client_id",
				"client_secret",
				"scopes",
				"auto_create",
				"enabled",
				backupColCreatedAtMs,
				"updated_at_ms",
				"version",
				"mode",
				"authorization_url",
				"token_url",
				"userinfo_url",
				"subject_claim",
				"name_claim",
				"username_claim",
				"email_claim",
			},
		},
	}
}

// sqliteIdentityBackupTables lists the column lists of the identity tables
// (ADR 52, ADR 53): principals, groups, memberships, access tokens, roles,
// grants, the audit sink cursors and the token policy, in the order of
// backup.Tables.
//
//nolint:funlen // A constant table of column names; its length is the schema.
func sqliteIdentityBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "principals",
			columns: []string{
				"id",
				backupColSubject,
				backupColDisplayName,
				backupColKind,
				backupColSource,
				backupColCreatedAtMs,
				backupColUpdatedAtMs,
				backupColDeletedAtMs,
				"disabled_at_ms",
				"last_active_at_ms",
				"last_sign_in_at_ms",
				"namespace",
				"break_glass",
			},
		},
		{
			table: "principal_groups",
			columns: []string{
				"id",
				backupColName,
				backupColNameKey,
				"description",
				backupColSource,
				backupColCreatedAtMs,
				backupColUpdatedAtMs,
				backupColDeletedAtMs,
				"closed_to_identity_provider",
			},
		},
		{
			table: "group_members",
			columns: []string{
				backupColGroupID,
				backupColPrincipalID,
				"added_at_ms",
				"removed_at_ms",
				backupColSource,
				backupColSourceID,
				backupColState,
				"suspended_at_ms",
				"pinned",
			},
		},
		{
			table: "group_mappings",
			columns: []string{
				backupColSource,
				backupColSourceID,
				"claim_value",
				backupColGroupID,
				backupColCreatedBy,
				backupColCreatedAtMs,
			},
		},
		{
			table: "member_removal_alerts",
			columns: []string{
				backupColSourceKind,
				backupColSourceID,
				"alerted_at_ms",
			},
		},
		{
			table: "member_removals",
			columns: []string{
				"id",
				backupColSourceKind,
				backupColSourceID,
				backupColGroupID,
				"removed",
				"members_before",
				backupColAtMs,
			},
		},
		{
			table: "temp_admins",
			columns: []string{
				backupColPrincipalID,
				backupColSubject,
				backupColExpiresAtMs,
				backupColCreatedAtMs,
				backupColCreatedBy,
				backupColReason,
				"ended_at_ms",
				"review_opened_at_ms",
			},
		},
		{
			table: "access_tokens",
			columns: []string{
				"id",
				backupColPrincipalID,
				backupColName,
				backupColKind,
				backupColDigest,
				backupColCreatedAtMs,
				backupColExpiresAtMs,
				"last_used_at_ms",
				backupColRevokedAtMs,
				"replaced_by",
				backupColCreatedBy,
				"disabled_at_ms",
			},
		},
		{
			table: "roles",
			columns: []string{
				"id",
				backupColName,
				backupColNameKey,
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
				backupColObject,
				"target",
				"object_kind",
				backupColNamespace,
				"condition",
				backupColCreatedAtMs,
				backupColCreatedBy,
				backupColRevokedAtMs,
				"revoked_by",
			},
		},
		{
			table: "audit_sink_cursors",
			columns: []string{
				"sink",
				backupColNamespace,
				backupColSequence,
				backupColUpdatedAtMs,
			},
		},
		{
			table: "audit_sink_leases",
			columns: []string{
				"sink",
				"holder",
				backupColExpiresAtMs,
			},
		},
		{
			table: "access_token_policy",
			columns: []string{
				"id",
				"max_lifetime_ms",
				"inactivity_ms",
				"disable_tokens",
				"disable_accounts",
				"disable_break_glass",
				backupColUpdatedAtMs,
				"exchange_lifetime_ms",
			},
		},
		{
			table:   "session_policy",
			columns: []string{"id", "idle_ms", "max_length_ms", backupColUpdatedAtMs, "token_freshness_ms"},
		},
	}
}
