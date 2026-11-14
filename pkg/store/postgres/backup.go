package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/internal/blobrefs"
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
	pgColGroupID     = "group_id"
	pgColSourceID    = "source_id"
	pgColSourceKind  = "source_kind"
	pgColDigest      = "digest"
	pgColEndedAtMs   = "ended_at_ms"
	pgColError       = "error"
	pgColName        = "name"
	pgColKey         = "key"
	pgColIssuer      = "issuer"
	pgColNamespace   = "namespace"
	pgColNamespaceID = "namespace_id"
	pgColReason      = "reason"
	pgColSeq         = "seq"
	pgColBlobKey     = "blob_key"
	pgColSequence    = "sequence"
	pgColSize        = "size"
	pgColStackID     = "stack_id"
	pgColWrapperID   = "wrapper_id"
	pgColScopeID     = "scope_id"
	pgColStartedAtMs = "started_at_ms"
	pgColState       = "state"
	pgColStatus      = "status"
	pgColUpdateID    = "update_id"
	pgColVersion     = "version"
	pgColUpdatedAtMs = "updated_at_ms"
	pgColDeletedAtMs = "deleted_at_ms"
	pgColSource      = "source"
	pgColKind        = "kind"
	pgColUsedAtMs    = "used_at_ms"
	pgColTarget      = "target"
	pgColObject      = "object"
	pgColSubject     = "subject"
	pgColDeletedName = "deleted_name"
	pgColTool        = "tool"
	pgColPosition    = "position"
	pgColProject     = "project"
	pgColJobID       = "job_id"
	pgColObjectKind  = "object_kind"
	pgColObjectID    = "object_id"
	pgColResourceKey = "resource_key"
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
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
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
	tables = append(tables, pgCopyCollectionTables()...)
	tables = append(tables, pgCopyGateTables()...)
	tables = append(tables, pgInventoryChangeTables()...)
	tables = append(tables, pgCopyWorkspaceTables()...)
	tables = append(tables, pgCopyIdentityTables()...)
	tables = append(tables, pgCopySessionTables()...)
	tables = append(tables, pgCopyImportTables()...)
	tables = append(tables, pgCopyBlameTables()...)
	tables = append(tables, pgCopyRunnerTables()...)
	tables = append(tables, pgCopyJobTables()...)
	tables = append(tables, pgCopyProfileTables()...)
	tables = append(tables, pgCopyManualLockTables()...)
	tables = append(tables, pgCopyPreferenceTables()...)
	tables = append(tables, pgCopyForgeLinkTables()...)
	tables = append(tables, pgCopyLocalUserTables()...)
	tables = append(tables, pgCopySavedViewTables()...)
	tables = append(tables, pgCopyDriftTables()...)
	tables = append(tables, pgCopyUnitSettingsTables()...)
	tables = append(tables, pgCopyGitHostTables()...)
	tables = append(tables, pgCopyRegistryTables()...)
	tables = append(tables, pgCopySinkTables()...)
	tables = append(tables, pgCopyTriggerTables()...)
	tables = append(tables, pgCopyItemKeyTables()...)
	tables = append(tables, pgCopySettingTables()...)
	tables = append(tables, pgCopySelfServiceTables()...)
	tables = append(tables, pgCopyTrustRuleTables()...)
	tables = append(tables, pgCopyBreakGlassReviewTables()...)

	return tables
}

// pgCopyStackTables holds the COPY columns of the namespace and stack
// tables, in foreign-key order.
func pgCopyStackTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "namespaces",
			columns: []string{
				pgColName, "primary_installation", "failover_version", pgColCreatedAtMs,
				"allow_forced_writes", "allow_unlocked_writes", "ip_allowlist", "id", "output_sharing_default",
				"source_filter",
			},
			orderBy: []string{pgColName},
		},
		{
			table: "stacks",
			columns: []string{
				pgColNamespace, pgColProject, pgColName, "id", pgColVersion, pgColCreatedAtMs, pgColUpdatedAtMs,
				pgColDeletedAtMs, pgColDeletedName,
			},
			orderBy: []string{pgColNamespace, pgColProject, pgColName},
		},
		{
			table:   "stack_tags",
			columns: []string{pgColStackID, pgColName, "value"},
			orderBy: []string{pgColStackID, pgColName},
		},
		{
			table: "updates",
			columns: []string{
				pgColStackID, "id", pgColKind, "dry_run", pgColStatus, pgColVersion, pgColRevision,
				"lease_digest", "lease_expires_at_ms", pgColCreatedAtMs, pgColStartedAtMs, pgColEndedAtMs,
				pgColCreatedBy, "restored_from", "journal_version", "journal_closed", "token_id",
				"journal_records_changed", pgColJobID, "git_head",
			},
			orderBy: []string{pgColStackID, "id"},
		},
		{
			table: "checkpoints",
			columns: []string{
				pgColStackID, pgColUpdateID, "lease_digest", "schema_version", pgColSequence,
				pgColDigest, "invalid", "features", "saved_at_ms",
				pgColBlobKey, pgColSize, "blob_size", "blob_md5",
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
				pgColStackID, pgColUpdateID, pgColArrival, pgColBlobKey, pgColSHA256, "md5", pgColSize,
				"lowest_sequence", "sequence_ids", "operation_ids",
			},
			orderBy: []string{pgColStackID, pgColUpdateID, pgColArrival},
		},
		{
			table:   "update_journal_sequences",
			columns: []string{pgColStackID, pgColUpdateID, pgColSequence, pgColArrival, pgColSHA256},
			orderBy: []string{pgColStackID, pgColUpdateID, pgColSequence},
		},
		{
			table: "update_journal_refusals",
			columns: []string{
				pgColStackID, pgColUpdateID, pgColArrival, "sequence_ids", "status", pgColReason,
			},
			orderBy: []string{pgColStackID, pgColUpdateID, pgColArrival},
		},
	}
}

// pgInventoryChangeTables holds the COPY columns of the inventory change log.
func pgInventoryChangeTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table:   "inventory_change_heads",
			columns: []string{pgColNamespaceID, "last_seq"},
			orderBy: []string{pgColNamespaceID},
		},
		{
			table: "inventory_changes",
			columns: []string{
				pgColNamespaceID, pgColSeq, pgColAtMs, pgColTool, pgColUnitID, pgColName, pgColDisplayName,
				pgColStatus, "collection", pgColKind, pgColResourceCount,
			},
			orderBy: []string{pgColNamespaceID, pgColSeq},
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
				pgColNamespace, pgColStackID, pgColWrapperID, "ciphertext", pgColCreatedAtMs,
			},
			orderBy: []string{pgColNamespace, pgColStackID},
		},
		{
			table: "inventory",
			columns: []string{
				pgColNamespace, pgColTool, pgColName, pgColUnitID, pgColDisplayName,
				"last_changed_ms", pgColStatus, "resource_count",
				"lock_holder", "lock_since_ms", "lock_operation",
				"last_release_lock_id", "last_release_holder", "last_release_by", "last_release_kind",
				"last_release_at_ms",
			},
			orderBy: []string{pgColNamespace, pgColTool, pgColName},
		},
		{
			table:   "inventory_labels",
			columns: []string{pgColNamespace, pgColUnitID, "label_key", "label_value"},
			orderBy: []string{pgColUnitID, "label_key"},
		},
		{
			table:   "inventory_deleted",
			columns: []string{pgColNamespace, pgColTool, pgColUnitID, pgColName, pgColDeletedAtMs, "purge_after_ms"},
			orderBy: []string{pgColTool, pgColUnitID},
		},
	}
}

// pgCopyCollectionTables holds the COPY columns of the collection, context and
// blob purge tables, in foreign-key order.
func pgCopyCollectionTables() []pgCopyTable {
	return []pgCopyTable{
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
			table: "placement_rules",
			columns: []string{
				"id", pgColNamespace, pgColPosition, pgColTool, pgColProject, "stack", "collection_key",
				"created_by", pgColCreatedAtMs,
			},
			orderBy: []string{pgColNamespace, pgColPosition, "id"},
		},
		{
			table: "object_context",
			columns: []string{
				pgColNamespaceID,
				pgColObjectKind,
				pgColObjectID,
				pgColTool,
				pgColResourceKey,
				pgColSource,
				"fields",
				"warning",
				"item_id",
				"item_seq",
				"resource_deleted_at_ms",
				"marks_seq",
				"updated_at_ms",
				"updated_by",
			},
			orderBy: []string{pgColNamespaceID, pgColObjectKind, pgColObjectID, pgColResourceKey, pgColSource},
		},
		{
			table: "object_context_owners",
			columns: []string{
				pgColNamespaceID, pgColOwner, pgColObjectKind, pgColObjectID, pgColResourceKey, pgColSource,
			},
			orderBy: []string{
				pgColNamespaceID,
				pgColOwner,
				pgColObjectKind,
				pgColObjectID,
				pgColResourceKey,
				pgColSource,
			},
		},
		{
			table:   "blob_purges",
			columns: []string{pgColTool, pgColUnitID, pgColNamespaceID, "purged_at_ms"},
			orderBy: []string{pgColTool, pgColUnitID},
		},
	}
}

// pgCopyWorkspaceTables holds the COPY columns of the workspace tables, in
// foreign-key order.
func pgCopyWorkspaceTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "workspaces",
			columns: []string{
				"id", pgColNamespace, pgColName, pgColRevision, "lock_id", "lock_holder",
				"lock_reason", "locked_at_ms", "current_state_version_id", pgColCreatedAtMs, pgColUpdatedAtMs,
				"allow_forced_writes", "allow_unlocked_writes", pgColDeletedAtMs, pgColDeletedName,
				"last_release_lock_id", "last_release_holder", "last_release_by", "last_release_kind",
				"last_release_at_ms",
			},
			orderBy: []string{"id"},
		},
		{
			table: "state_versions",
			columns: []string{
				"id", pgColWorkspaceID, pgColStatus, "serial", "lineage", "md5", pgColSize,
				"content_key", "outputs", "force", "upload_digest", "upload_expires_at_ms",
				"holder", "lock_id", pgColCreatedAtMs, "finalized_at_ms", "restored_from", "sealed",
				"sensitive_marks", "fenced_by", "unlocked_write", "origin_job",
			},
			orderBy: []string{"id"},
		},
		{
			table: "audit_entries",
			columns: []string{
				pgColNamespace, pgColSequence, pgColSubject, "idp_subject", "token_id", "action",
				pgColObject, pgColVersion, "value_path", pgColAtMs, "source_ip", "user_agent",
				"channel", pgColKind, "outcome", "request_id", "prev_hash", "hash", "value_count",
				"changes", "object_id", "fence_version", "principal_kind", pgColJobID, pgColReason,
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
	}
}

// pgControlTables lists the column list of every table of
// backup.ControlTables: the durable tables live in the live schema, the
// installation, backups and restores tables in the control schema. CheckCoverage
// compares each list with the live columns, so a migration that adds a column
// fails until its author adds the column here.
func pgControlTables() []pgCopyTable {
	core := []pgCopyTable{
		{
			table: backup.TableDurableOperations,
			columns: []string{
				"id", "start_key", "kind", "input", pgColVersion, "state", "attempt", "expiries", "failures",
				"last_error", "claim_owner", "claim_token", "claim_expires_at_ms", "cancel_requested",
				"not_before_ms", "created_at_ms", "updated_at_ms",
			},
			orderBy: []string{"id"},
		},
		{
			table:   backup.TableDurableSteps,
			columns: []string{"operation_id", pgColName, pgColSeq, "result", "done_at_ms"},
			orderBy: []string{"operation_id", pgColSeq},
		},
		{
			table:   backup.TableDurableSchedules,
			columns: []string{pgColName, "interval_ms", "next_run_at_ms", "last_claimed_at_ms"},
			orderBy: []string{pgColName},
		},
		{
			table:   backup.TableSignInFailures,
			columns: []string{"source", "window_end_ms", "count"},
			orderBy: []string{"source", "window_end_ms"},
		},
		{
			table:   backup.TableAuditChainCheck,
			columns: []string{"id", "checked_at_ms", "invalid"},
			orderBy: []string{"id"},
		},
		{
			table:   backup.TableAuditChainMark,
			columns: []string{"scope", "epoch", "sequence", "marked_at_ms"},
			orderBy: []string{"scope"},
		},
		{
			table:   backup.TableBackups,
			columns: []string{"id", pgColState, pgColStartedAtMs, pgColEndedAtMs, pgColTarget, "error"},
			orderBy: []string{"id"},
		},
		{
			table: backup.TableRestores,
			columns: []string{
				"id", pgColState, "backup_id", "pre_backup_id", "requested_by",
				pgColStartedAtMs, pgColEndedAtMs, "error", "dev_report", "maintenance_entered", "audit_events",
			},
			orderBy: []string{pgColStartedAtMs, "id"},
		},
		{
			table:   backup.TableInstallation,
			columns: []string{"id", pgColState, "kind"},
			orderBy: []string{"id"},
		},
		{
			table:   backup.TableMaintenanceRevocations,
			columns: []string{"id", pgColKind, pgColTarget, "end_reason", "actor", pgColObject, pgColAtMs},
			orderBy: []string{"id"},
		},
		{
			table:   backup.TableInstanceVersions,
			columns: []string{"id", "binary_version", pgColStartedAtMs},
			orderBy: []string{"id"},
		},
	}

	return slices.Concat(core, pgStoreCheckControlTables(), pgRunnerControlTables(), pgNameLockControlTables())
}

// pgNameLockControlTables holds the COPY columns of the counts of the
// lockout by name and of the key of their names.
func pgNameLockControlTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: backup.TableNameLocks,
			columns: []string{
				sinkColScope, pgColName, "sub", "failures", "locked_until_ms", "level", "last_failure_ms",
				"last_lock_end_ms", "reset_after_ms",
			},
			orderBy: []string{sinkColScope, pgColName, "sub"},
		},
		{
			table:   backup.TableNameLockKey,
			columns: []string{"id", pgColKey},
			orderBy: []string{"id"},
		},
		{
			table:   backup.TableUsedTickets,
			columns: []string{"provider_id", "ticket_digest", "until_ms"},
			orderBy: []string{"provider_id", "ticket_digest"},
		},
	}
}

// pgStoreCheckControlTables lists the column list of the store check rows,
// which live in the control schema.
func pgStoreCheckControlTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: backup.TableStoreChecks,
			columns: []string{
				"id", pgColTarget, pgColState, "requested_by", pgColStartedAtMs, pgColEndedAtMs, pgColError, "report",
			},
			orderBy: []string{"id"},
		},
	}
}

// pgEmptiedTables lists the column list of every table of
// backup.EmptiedTables. CheckCoverage compares each list with the live
// columns.
func pgEmptiedTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: backup.TableIdempotencyRecords,
			columns: []string{
				"principal", "method", "path", "idem_key", "fingerprint", "token", "status", "headers", pgColBody,
				pgColCreatedAtMs,
			},
			orderBy: []string{"principal", "method", "path", "idem_key"},
		},
		{
			table:   backup.TableIdempotencyServerKey,
			columns: []string{"id", "wrapped_key", pgColWrapperID},
			orderBy: []string{"id"},
		},
		{
			table:   backup.TableDurableHeartbeats,
			columns: []string{pgColOwner, pgColAtMs, "lease_ms"},
			orderBy: []string{pgColOwner},
		},
		{
			table:   backup.TableLocalUserResets,
			columns: pgLocalUserResetColumns(),
			orderBy: []string{"id"},
		},
		{
			table:   backup.TablePasskeyChallenges,
			columns: pgPasskeyChallengeColumns(),
			orderBy: []string{pgColDigest},
		},
		{
			table: backup.TableUnitState,
			columns: []string{
				pgColUnitID, pgColNamespaceID, "version_id", "final_version_id", "final_commit", "final_job",
				"final_at_ms",
			},
			orderBy: []string{pgColUnitID},
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
				"ended_at_ms", "end_reason", "source_ip", "user_agent", "generation",
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
			columns: []string{pgColProviderID, pgColIssuer, pgColCreatedAtMs},
			orderBy: []string{pgColProviderID},
		},
		{
			table:   "installation_secret_keys",
			columns: []string{pgColScopeID, pgColWrapperID, "ciphertext", pgColCreatedAtMs},
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
				pgColDeletedAtMs, "closed_to_identity_provider",
			},
			orderBy: []string{"id"},
		},
		{
			table: backup.TableGroupMembers,
			columns: []string{
				pgColGroupID, pgColPrincipalID, "added_at_ms", "removed_at_ms", pgColSource, pgColSourceID,
				pgColState, "suspended_at_ms", "pinned",
			},
			orderBy: []string{pgColGroupID, pgColPrincipalID, "added_at_ms"},
		},
		{
			table: backup.TableGroupMappings,
			columns: []string{
				pgColSource, pgColSourceID, "claim_value", pgColGroupID, pgColCreatedBy, pgColCreatedAtMs,
			},
			orderBy: []string{pgColSource, pgColSourceID, "claim_value", pgColGroupID},
		},
		{
			table: "member_removal_alerts",
			columns: []string{
				pgColSourceKind, pgColSourceID, "alerted_at_ms",
			},
			orderBy: []string{pgColSourceKind, pgColSourceID},
		},
		{
			table: "member_removals",
			columns: []string{
				"id", pgColSourceKind, pgColSourceID, pgColGroupID, "removed", "members_before", pgColAtMs,
			},
			orderBy: []string{"id"},
		},
		{
			table: backup.TableTempAdmins,
			columns: []string{
				pgColPrincipalID, pgColSubject, pgColExpiresAtMs, pgColCreatedAtMs, pgColCreatedBy, pgColReason,
				"ended_at_ms", "review_opened_at_ms",
			},
			orderBy: []string{pgColPrincipalID},
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
				"id", pgColSubject, "subject_kind", "subject_id", "role", pgColObject, "target", "object_kind",
				pgColNamespace, "condition", pgColCreatedAtMs, pgColCreatedBy, "revoked_at_ms", "revoked_by",
			},
			orderBy: []string{"id"},
		},
		{
			table: "access_token_policy",
			columns: []string{
				"id", "max_lifetime_ms", "inactivity_ms", "disable_tokens", "disable_accounts",
				"disable_break_glass", pgColUpdatedAtMs, "exchange_lifetime_ms",
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

// pgControlSpec returns the column list of table, which must be in
// backup.ControlTables.
func pgControlSpec(table string) (pgCopyTable, error) {
	for _, spec := range pgControlTables() {
		if spec.table == table {
			return spec, nil
		}
	}

	return pgCopyTable{}, fmt.Errorf("control table %q has no column list: %w", table, backup.ErrCoverage)
}

// CheckCoverage implements backup.Coverage: it compares the COPY column list of
// each table of backup.Tables and the column list of each table of
// backup.ControlTables with the columns that the schema holds for the table.
// A table without a list, a column that differs, or a live table in neither
// list fails with backup.ErrCoverage.
func (s *Store) CheckCoverage(ctx context.Context) error {
	for _, table := range backup.Tables {
		spec, err := pgCopySpec(table)
		if err != nil {
			return err
		}

		if checkErr := s.checkColumns(ctx, spec); checkErr != nil {
			return checkErr
		}
	}

	for _, table := range backup.ControlTables {
		spec, err := pgControlSpec(table)
		if err != nil {
			return err
		}

		if checkErr := s.checkColumns(ctx, spec); checkErr != nil {
			return checkErr
		}
	}

	for _, spec := range pgEmptiedTables() {
		if err := s.checkColumns(ctx, spec); err != nil {
			return err
		}
	}

	if err := s.checkNoUnknownTables(ctx); err != nil {
		return err
	}

	return s.checkPortableColumns(ctx)
}

// checkColumns compares the column list of spec with the live columns of its
// table.
func (s *Store) checkColumns(ctx context.Context, spec pgCopyTable) error {
	live, err := s.liveColumns(ctx, spec.table)
	if err != nil {
		return err
	}

	return backup.CheckColumns(spec.table, spec.columns, live)
}

// checkNoUnknownTables fails with ErrCoverage when the schema holds a data
// table in neither backup.Tables nor backup.ControlTables.
func (s *Store) checkNoUnknownTables(ctx context.Context) error {
	live, err := s.SchemaTables(ctx)
	if err != nil {
		return err
	}

	known := make(map[string]bool, len(backup.Tables)+len(backup.ControlTables)+len(backup.EmptiedTables))
	for _, table := range backup.Tables {
		known[table] = true
	}

	for _, table := range backup.ControlTables {
		known[table] = true
	}

	for _, table := range backup.EmptiedTables {
		known[table] = true
	}

	for _, table := range live {
		if !known[table] {
			return fmt.Errorf("schema holds table %q in neither the backup nor the control list: %w",
				table, backup.ErrCoverage)
		}
	}

	return nil
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

// liveColumns returns the column names of table in the live or the control
// schema, in ordinal order. The control schema appends _control to the
// current schema, so each live schema keeps its own control tables.
func (s *Store) liveColumns(ctx context.Context, table string) ([]string, error) {
	rows, err := s.reads.Query(ctx,
		`SELECT column_name FROM information_schema.columns`+
			` WHERE table_schema IN (current_schema(), current_schema() || '_control')`+
			` AND table_name = $1 ORDER BY ordinal_position`,
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
//
// Defense in depth: Open refuses a live schema name that checkIdent refuses,
// and the other names come from the code. pgIdent still doubles each double
// quote, so that a name from a new caller that skips checkIdent cannot end
// the quoted identifier.
func pgIdent(name string) string {
	return pgx.Identifier{name}.Sanitize()
}

// errIdentifier reports a schema, table or column name that is not a plain
// SQL identifier.
var errIdentifier = errors.New("name is not a plain SQL identifier")

// plainIdent matches a plain SQL identifier: a letter or an underscore, then
// letters, digits and underscores.
var plainIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// checkIdent returns errIdentifier when name is not a plain SQL identifier.
func checkIdent(name string) error {
	if !plainIdent.MatchString(name) {
		return errIdentifier
	}

	return nil
}

// pgAttrs is the manifest subset the SQL data determines, without row counts.
type pgAttrs struct {
	installations []string
	wrappers      []string
	blobs         []backup.BlobRef
	// unverified holds the keys that rows name and the backup does not copy.
	unverified []string
	version    int
}

// backupAttrs reads the primary installations, wrapper IDs, blob references and
// schema version. Export calls it in the capture transaction, so the values
// match the captured rows; summarize calls it after the load.
func backupAttrs(ctx context.Context, q *db.Queries, qr pgQuerier) (pgAttrs, error) {
	installations, err := q.BackupPrimaryInstallations(ctx)
	if err != nil {
		return pgAttrs{}, fmt.Errorf("select backup installations: %w", err)
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

	collector := &blobrefs.Collector{}
	if addErr := addStoredBlobRefs(ctx, qr, pgIdent, collector); addErr != nil {
		return pgAttrs{}, addErr
	}

	blobs = append(blobs, collector.Refs()...)
	slices.SortFunc(blobs, func(a, b backup.BlobRef) int { return strings.Compare(a.Key, b.Key) })

	var version int64

	// schema_migrations is runtime bookkeeping, not a migration, so no sqlc
	// query covers it; the adapter reads it directly.
	if err := qr.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return pgAttrs{}, fmt.Errorf("read schema version: %w", err)
	}

	if installations == nil {
		installations = []string{}
	}

	if wrappers == nil {
		wrappers = []string{}
	}

	return pgAttrs{
		installations: installations,
		wrappers:      wrappers,
		blobs:         blobs,
		unverified:    collector.Unverified(),
		version:       int(version),
	}, nil
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
// loads its tables, the manifest summary and the time of the restore.
type stagingInput struct {
	load      func(ctx context.Context, schema string) error
	want      backup.Summary
	staging   string
	restoreAt time.Time
}

// buildStaging creates the staging schema, loads and checks the dump, and keeps
// the schema as the staging area. A failure drops the schema: the live tables
// stay unchanged.
func (s *Store) buildStaging(ctx context.Context, in stagingInput) (backup.Summary, error) {
	sum, err := s.buildChecked(ctx, in)
	if err != nil {
		return backup.Summary{}, err
	}

	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()

	if s.stagingSchema != "" {
		if dropErr := s.dropStagingSchema(context.WithoutCancel(ctx), s.stagingSchema); dropErr != nil {
			// The new schema is not kept, so it goes too.
			return backup.Summary{}, errors.Join(dropErr, s.dropStagingSchema(context.WithoutCancel(ctx), in.staging))
		}
	}

	s.stagingSchema = in.staging

	return sum, nil
}

// buildChecked creates the staging schema in.staging, loads and checks the
// dump, and returns its summary. A failure drops the schema: the live tables
// stay unchanged.
func (s *Store) buildChecked(ctx context.Context, in stagingInput) (backup.Summary, error) {
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

	checkErr := s.inLongTx(ctx, backupBound(), defaultTx(), func(tx pgx.Tx) error {
		return checkStaged(ctx, tx, in.staging, in.restoreAt)
	})
	if checkErr != nil {
		return backup.Summary{}, checkErr
	}

	sum, err := s.summarizeStaging(ctx, in.staging, in.want)
	if err != nil {
		return backup.Summary{}, err
	}

	if blobErr := checkStagedBlobData(sum, in.want); blobErr != nil {
		return backup.Summary{}, blobErr
	}

	kept = true

	return sum, nil
}

// checkStaged runs the checks of the staged rows in q: the staging name, the
// sealed state, the namespace IDs, the owned rows, the grants and the
// memberships of principals of a namespace, the removal alert markers and the
// feature rows.
func checkStaged(ctx context.Context, q pgQuerier, staging string, restoreAt time.Time) error {
	if identErr := checkStagingIdent(staging); identErr != nil {
		return identErr
	}

	if sealedErr := checkStagedSealedState(ctx, q, staging); sealedErr != nil {
		return sealedErr
	}

	if idsErr := checkStagedNamespaceIDs(ctx, q, staging); idsErr != nil {
		return idsErr
	}

	if ownedErr := checkStagedOwnedRows(ctx, q, staging); ownedErr != nil {
		return ownedErr
	}

	if grantErr := checkStagedNamespacePrincipalGrants(ctx, q, staging); grantErr != nil {
		return grantErr
	}

	if memberErr := checkStagedNamespacePrincipalMemberships(ctx, q, staging); memberErr != nil {
		return memberErr
	}

	if markerErr := checkStagedRemovalAlerts(ctx, q, staging, restoreAt); markerErr != nil {
		return markerErr
	}

	return checkStagedFeatureRows(ctx, q, staging)
}

// summarizeStaging reads the summary of the staging schema: the dialect, the
// schema version the staging schema_migrations records, the primary installations, the
// wrapper IDs, the row count of each table, and the blob references. The live
// tables stay out of every query: each one names the staging schema. A staged
// version that differs from the manifest reports ErrCorrupt.
func (s *Store) summarizeStaging(
	ctx context.Context, staging string, want backup.Summary,
) (backup.Summary, error) {
	var sum backup.Summary

	err := s.inLongTx(ctx, backupBound(), defaultTx(), func(tx pgx.Tx) error {
		var readErr error

		sum, readErr = summarizeStagingIn(ctx, tx, staging, want)

		return readErr
	})
	if err != nil {
		return backup.Summary{}, err
	}

	return sum, nil
}

// summarizeStagingIn is summarizeStaging in q.
func summarizeStagingIn(
	ctx context.Context, q pgQuerier, staging string, want backup.Summary,
) (backup.Summary, error) {
	installations, err := stagingStrings(ctx, q, staging, "namespaces", "primary_installation")
	if err != nil {
		return backup.Summary{}, err
	}

	wrappers, err := stagingWrapperIDs(ctx, q, staging)
	if err != nil {
		return backup.Summary{}, err
	}

	counts := make(map[string]int64, len(backup.Tables))

	for _, table := range backup.Tables {
		count, countErr := stagingCount(ctx, q, staging, table)
		if countErr != nil {
			return backup.Summary{}, countErr
		}

		counts[table] = count
	}

	blobs, err := stagingBlobRefs(ctx, q, staging)
	if err != nil {
		return backup.Summary{}, err
	}

	stagedTable := func(table string) string { return stagingTable(staging, table) }

	collector := &blobrefs.Collector{}
	if addErr := addStoredBlobRefs(ctx, q, stagedTable, collector); addErr != nil {
		return backup.Summary{}, addErr
	}

	blobs = append(blobs, collector.Refs()...)

	version, err := stagingSchemaVersion(ctx, q, staging)
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
		Dialect:              backup.DialectPostgres,
		PrimaryInstallations: installations,
		WrapperIDs:           wrappers,
		RowCounts:            counts,
		Blobs:                blobs,
		Unverified:           collector.Unverified(),
		SchemaVersion:        version,
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
// migration DDL runs with the search path on the staging schema and its own
// control schema: the live tables stay untouched. It records every migration in the staging
// schema_migrations, so the staged data carries its own schema version.
func (s *Store) createStagingSchema(ctx context.Context, staging string) error {
	all, err := migrate.Load(migrations, "migrations")
	if err != nil {
		return err
	}

	if err := s.createSchema(ctx, staging); err != nil {
		return fmt.Errorf("create staging schema: %w", err)
	}

	opts := pgx.TxOptions{
		IsoLevel:       pgx.ReadCommitted,
		AccessMode:     pgx.ReadWrite,
		DeferrableMode: pgx.NotDeferrable,
		BeginQuery:     "",
		CommitQuery:    "",
	}

	return s.inLongTx(ctx, backupBound(), opts, func(tx pgx.Tx) error {
		// The control schema of the staging schema comes second. The migration
		// that moves the installation table makes it, and a later migration
		// that changes a control table finds the table through it.
		path := pgIdent(staging) + ", " + pgIdent(staging+"_control")
		if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+path); err != nil {
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
func stagingSchemaVersion(ctx context.Context, pool pgQuerier, staging string) (int, error) {
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
func checkStagedSealedState(ctx context.Context, pool pgQuerier, staging string) error {
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

// checkStagedNamespaceIDs refuses a staged backup with a namespace whose ID is
// not a valid namespace ID. It returns ErrCorrupt.
func checkStagedNamespaceIDs(ctx context.Context, pool pgQuerier, staging string) error {
	rows, err := pool.Query(ctx, "SELECT name, id FROM "+stagingTable(staging, "namespaces"))
	if err != nil {
		return fmt.Errorf("read staged namespaces: %w", backup.ErrExportRead)
	}

	defer rows.Close()

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

// stagingWrapperIDs returns the distinct wrapper IDs of the namespace keys and
// of the installation keys and the item keys that are not shredded in the staging
// schema, ordered, never nil.
func stagingWrapperIDs(ctx context.Context, pool pgQuerier, staging string) ([]string, error) {
	keys, err := stagingStrings(ctx, pool, staging, "secret_keys", pgColWrapperID)
	if err != nil {
		return nil, err
	}

	installation, err := stagingStrings(ctx, pool, staging, "installation_secret_keys", pgColWrapperID)
	if err != nil {
		return nil, err
	}

	items, err := stagingItemKeyWrapperIDs(ctx, pool, staging)
	if err != nil {
		return nil, err
	}

	out := slices.Concat(keys, installation, items)
	slices.Sort(out)

	return slices.Compact(out), nil
}

// stagingStrings returns the distinct values of column in the staging table,
// ordered, never nil.
func stagingStrings(
	ctx context.Context, pool pgQuerier, staging, table, column string,
) ([]string, error) {
	return queryStrings(
		ctx,
		pool,
		"SELECT DISTINCT "+pgIdent(column)+" FROM "+stagingTable(staging, table)+" ORDER BY "+pgIdent(column),
	)
}

// stagingItemKeyWrapperIDs returns the distinct wrapper IDs of the item keys
// in the staging schema that are not shredded, ordered, never nil. A shredded
// key has no key blob left to open, so its wrapper does not count.
func stagingItemKeyWrapperIDs(ctx context.Context, pool pgQuerier, staging string) ([]string, error) {
	return queryStrings(
		ctx, pool, "SELECT DISTINCT wrapper_id FROM "+stagingTable(staging, "item_keys")+
			" WHERE shredded_at_ms = 0 ORDER BY wrapper_id",
	)
}

// queryStrings runs a query that returns one text column, and returns the
// values, never nil. The query text comes from this package, never from the
// network.
func queryStrings(ctx context.Context, pool pgQuerier, query string) ([]string, error) {
	rows, err := pool.Query(ctx, query)
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
func stagingCount(ctx context.Context, pool pgQuerier, staging, table string) (int64, error) {
	var count int64

	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+stagingTable(staging, table)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count staged table %s: %w", table, err)
	}

	return count, nil
}

// stagingBlobRefs reads the blob references of the staging schema: every blob
// key the staged finalized state versions, the staged checkpoints, the staged journal
// batches and the staged item keys that are not shredded name, with the size and MD5 of the plain bytes, ordered by
// key.
// Sealed state versions mark their references sealed; checkpoint and journal
// blobs are never sealed. A pending version has no committed content, so the
// backup leaves pending rows out. It matches BackupBlobs, so the staged summary
// compares equal with the manifest.
func stagingBlobRefs(ctx context.Context, pool pgQuerier, staging string) ([]backup.BlobRef, error) {
	rows, err := pool.Query(
		ctx,
		"SELECT content_key, size, md5, sealed FROM "+stagingTable(staging, "state_versions")+
			" WHERE content_key <> '' AND status = 'finalized' UNION ALL SELECT blob_key AS content_key, blob_size AS size, "+
			"blob_md5 AS md5, FALSE AS sealed FROM "+stagingTable(staging, "checkpoints")+
			" UNION ALL SELECT blob_key AS content_key, size AS size, "+
			"md5 AS md5, FALSE AS sealed FROM "+stagingTable(staging, "update_journal")+
			" UNION ALL SELECT 'namespaces/' || namespace_id || '/item-keys/' || id || '/v' || blob_version::text "+
			"|| '-' || blob_attempt AS content_key, blob_size AS size, blob_md5 AS md5, FALSE AS sealed FROM "+
			stagingTable(staging, "item_keys")+" WHERE shredded_at_ms = 0"+
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

// checkStagedBlobData compares the blobs that the staged data names with the
// manifest: the references must match, and every blob that the staged data
// names without a check value must be one that the manifest lists as not
// copied.
func checkStagedBlobData(staged, want backup.Summary) error {
	if err := checkStagedBlobRefs(staged.Blobs, want.Blobs); err != nil {
		return err
	}

	if err := backup.CheckUnverified(staged.Unverified, want.Unverified); err != nil {
		return fmt.Errorf("staged backup: %w", err)
	}

	return nil
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

// createSchema creates the schema name in a short transaction with the bounds
// of a schema change.
func (s *Store) createSchema(ctx context.Context, name string) error {
	return s.inLongTx(ctx, schemaBound(), defaultTx(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "CREATE SCHEMA "+pgIdent(name)); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}

		return nil
	})
}

// dropStagingSchema removes the staging schema with its tables and the
// control schema that its migration made. The name comes from
// newStagingSchema, never from the network; the prefix check keeps a
// programming error from dropping a live schema.
func (s *Store) dropStagingSchema(ctx context.Context, staging string) error {
	if !strings.HasPrefix(staging, stagingSchemaPrefix) {
		return fmt.Errorf("drop staging schema %q: %w", staging, backup.ErrCorrupt)
	}

	return s.inLongTx(ctx, schemaBound(), defaultTx(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgIdent(staging)+" CASCADE"); err != nil {
			return fmt.Errorf("drop staging schema: %w", err)
		}

		if _, err := tx.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgIdent(staging+"_control")+" CASCADE"); err != nil {
			return fmt.Errorf("drop staging control schema: %w", err)
		}

		return nil
	})
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

	if err := s.reads.QueryRow(ctx, selectVersion).Scan(&version); err != nil {
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

// StartBackup implements backup.Registry: it stores r as a running backup and
// starts the operation start in one transaction. It reports ErrAlreadyExists
// when the ID is taken, and ErrAlreadyRunning when the unique index on
// running backups refuses the row. A failed start changes nothing.
func (s *Store) StartBackup(ctx context.Context, r backup.Record, start durable.StartRequest) error {
	if err := checkBackupStart(r, start); err != nil {
		return err
	}

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

	var op durable.Operation

	err := s.inMinimumTx(ctx, func(q *db.Queries) error {
		started, err := startBackupTx(ctx, q, r, start)
		if err != nil {
			return err
		}

		row, err := q.ClaimStartedDurableOperation(ctx, db.ClaimStartedDurableOperationParams{
			Owner: claim.Owner, LeaseMs: claim.Lease.Milliseconds(), ID: string(started.ID),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// The start created the operation in this transaction, so it is
			// pending: no row means that the claim cannot be taken.
			return fmt.Errorf("claim the started backup operation %s: %w", started.ID, durable.ErrClaimLost)
		}

		if err != nil {
			return fmt.Errorf("claim the started backup operation %s: %w", started.ID, translate(err))
		}

		op = operationFromRow(row, nil)

		return nil
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
	n, err := q.CreateBackup(ctx, db.CreateBackupParams{
		ID:          r.ID.String(),
		State:       string(backup.StateRunning),
		StartedAtMs: codec.Millis(r.StartedAt),
		EndedAtMs:   backupEndedAt(time.Time{}),
		Target:      r.Target,
		Error:       "",
	})
	if err != nil {
		return durable.Operation{}, fmt.Errorf("insert backup: %w", err)
	}

	if n == 0 {
		return durable.Operation{}, backupConflict(ctx, q, r.ID)
	}

	op, created, err := startOperation(ctx, q, start)
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
// its step and moves the running backup id to a finished state with the error
// text and end time, in one transaction, and returns the finished row. Any
// state but succeeded or failed reports ErrNotRunning, as do a row that is
// not running; a missing row reports ErrNotFound. A fence that is not the
// live claim reports the durable sentinel and changes nothing.
//
//nolint:revive // argument-limit: the fence, the row and the three fields of its end are one write; a struct would only move the fields.
func (s *Store) FinishBackup(
	ctx context.Context, f durable.StepFence, id backup.ID, state backup.State, errText string, endedAt time.Time,
) (backup.Record, error) {
	if state != backup.StateSucceeded && state != backup.StateFailed {
		return backup.Record{}, backup.ErrNotRunning
	}

	var out backup.Record

	err := s.inMinimumTx(ctx, func(q *db.Queries) error {
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

		n, err := q.FinishBackup(ctx, db.FinishBackupParams{
			State:     string(state),
			Error:     errText,
			EndedAtMs: backupEndedAt(endedAt),
			ID:        id.String(),
		})
		if err != nil {
			return fmt.Errorf("finish backup: %w", err)
		}

		if n == 0 {
			return finishFailure(ctx, q, id)
		}

		row, err := q.GetBackup(ctx, id.String())
		if err != nil {
			return fmt.Errorf("select finished backup: %w", err)
		}

		out = backupRecord(row)

		return nil
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

// finishFailure reports why FinishBackup changed nothing: ErrNotFound when id
// does not exist, ErrNotRunning when the row is not running.
func finishFailure(ctx context.Context, q *db.Queries, id backup.ID) error {
	if _, err := q.GetBackup(ctx, id.String()); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return backup.ErrNotFound
		}

		return fmt.Errorf("select backup: %w", err)
	}

	return backup.ErrNotRunning
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

// FailEndedBackups implements backup.Registry: it marks failed each running
// row whose operation ended failed or canceled. The error text is the last
// error of the operation. Open does not call it: a reconnect must preserve
// running rows, which the conformance suite replays.
func (s *Store) FailEndedBackups(ctx context.Context, endedAt time.Time) (int, error) {
	n, err := s.queries.FailEndedBackups(ctx, db.FailEndedBackupsParams{
		Fallback:  backup.EndedWithoutFinishText,
		EndedAtMs: backupEndedAt(endedAt),
		Prefix:    backup.OperationKeyPrefix,
		Kind:      string(backup.OperationKind),
	})
	if err != nil {
		return 0, fmt.Errorf("fail ended backups: %w", err)
	}

	return int(n), nil
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

// SchemaTables implements backup.Inspector: it reads the table catalog of
// the live and the control schemas, so a table missing from backup.Tables
// and backup.ControlTables fails the conformance suite. Bookkeeping
// (schema_migrations, and audit_chain_state, the chain state of this
// installation) is not data and stays out.
func (s *Store) SchemaTables(ctx context.Context) ([]string, error) {
	rows, err := s.reads.Query(ctx, `SELECT table_name FROM information_schema.tables`+
		` WHERE table_schema IN (current_schema(), current_schema() || '_control')`+
		` AND table_type = 'BASE TABLE'`+
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
	return backupConflict(ctx, s.queries, id)
}

// backupConflict is createBackupConflict for the queries of one transaction.
func backupConflict(ctx context.Context, q *db.Queries, id backup.ID) error {
	_, err := q.GetBackup(ctx, id.String())
	if err == nil {
		return backup.ErrAlreadyExists
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return backup.ErrAlreadyRunning
	}

	return fmt.Errorf("select backup: %w", err)
}

// checkStagedFeatureRows checks the rows of the staged features that have rules
// of their own: the drift results, the unit settings, the registry, the Git host connections,
// the notification sinks, the triggers, the self-service blueprints, the trust rules and the
// break-glass reviews.
func checkStagedFeatureRows(ctx context.Context, pool pgQuerier, staging string) error {
	if err := checkStagedDriftResults(ctx, pool, staging); err != nil {
		return err
	}

	if err := checkStagedJobs(ctx, pool, staging); err != nil {
		return err
	}

	if err := checkStagedUnitSettings(ctx, pool, staging); err != nil {
		return err
	}

	if err := checkStagedRegistry(ctx, pool, staging); err != nil {
		return err
	}

	if err := checkStagedGitHost(ctx, pool, staging); err != nil {
		return err
	}

	if err := checkStagedSinks(ctx, pool, staging); err != nil {
		return err
	}

	if err := checkStagedNotifications(ctx, pool, staging); err != nil {
		return err
	}

	if err := checkStagedTriggers(ctx, pool, staging); err != nil {
		return err
	}

	if err := checkStagedSelfServiceBlueprints(ctx, pool, staging); err != nil {
		return err
	}

	if err := checkStagedTrustRules(ctx, pool, staging); err != nil {
		return err
	}

	if err := checkStagedBreakGlassReviews(ctx, pool, staging); err != nil {
		return err
	}

	return checkStagedForgeLinks(ctx, pool, staging)
}
