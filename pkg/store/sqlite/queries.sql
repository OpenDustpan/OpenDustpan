-- name: CreateNamespace :execrows
INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
VALUES (?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetNamespace :one
SELECT name, home_region, failover_version, created_at_ms, allow_forced_writes, allow_unlocked_writes
FROM namespaces
WHERE name = ?;

-- name: ListNamespaces :many
SELECT name, home_region, failover_version, created_at_ms, allow_forced_writes, allow_unlocked_writes
FROM namespaces
ORDER BY name;

-- name: SetNamespaceOverrides :execrows
-- Replaces the override switches of one namespace (ADR 44, decision 6).
UPDATE namespaces
SET allow_forced_writes = ?, allow_unlocked_writes = ?
WHERE name = ?;

-- name: GetNamespaceIPAllowlist :one
SELECT ip_allowlist
FROM namespaces
WHERE name = ?;

-- name: SetNamespaceIPAllowlist :execrows
-- Replaces the IP allowlist of one namespace (ADR 55, decision 8).
UPDATE namespaces
SET ip_allowlist = ?
WHERE name = ?;

-- name: GetFailoverVersion :one
SELECT failover_version
FROM namespaces
WHERE name = ?;

-- name: CreateStack :execrows
INSERT INTO stacks (namespace, project, name, id, version, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetStack :one
SELECT namespace, project, name, id, version, created_at_ms, updated_at_ms
FROM stacks
WHERE namespace = ? AND project = ? AND name = ? AND deleted_at_ms IS NULL;

-- name: ListStacksInNamespace :many
SELECT namespace, project, name, id, version, created_at_ms, updated_at_ms
FROM stacks
WHERE namespace = ? AND deleted_at_ms IS NULL
ORDER BY project, name;

-- name: ListStacksInProject :many
SELECT namespace, project, name, id, version, created_at_ms, updated_at_ms
FROM stacks
WHERE namespace = ? AND project = ? AND deleted_at_ms IS NULL
ORDER BY name;

-- name: CreateStackTag :exec
INSERT INTO stack_tags (stack_id, name, value)
VALUES (?, ?, ?);

-- name: GetStackTags :many
SELECT name, value
FROM stack_tags
WHERE stack_id = ?
ORDER BY name;

-- name: ListStackTagsInNamespace :many
SELECT t.stack_id, t.name, t.value
FROM stack_tags AS t
JOIN stacks AS s ON s.id = t.stack_id
WHERE s.namespace = ? AND s.deleted_at_ms IS NULL
ORDER BY t.stack_id, t.name;

-- name: DeleteStackTags :exec
DELETE FROM stack_tags
WHERE stack_id = ?;

-- name: TouchStack :exec
UPDATE stacks
SET updated_at_ms = ?
WHERE id = ?;

-- name: RenameStack :execrows
UPDATE stacks SET name = ? WHERE namespace = ? AND project = ? AND name = ? AND deleted_at_ms IS NULL;

-- name: GetStackID :one
SELECT id
FROM stacks
WHERE namespace = ? AND project = ? AND name = ? AND deleted_at_ms IS NULL;

-- name: CreateUpdate :exec
INSERT INTO updates (
    stack_id, id, kind, dry_run, status, version, revision,
    lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms,
    created_by, restored_from
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetUpdate :one
-- The checkpoint size is the stored uncompressed size, or the byte length of
-- the saved deployment for rows from before blobs, read without loading the
-- document: the checkpoints table holds one row for each update, so the join
-- finds at most one row.
SELECT u.stack_id, u.id, u.kind, u.dry_run, u.status, u.version, u.revision,
       u.lease_digest, u.lease_expires_at_ms, u.created_at_ms, u.started_at_ms, u.ended_at_ms,
       u.created_by, u.restored_from, u.journal_version,
       CAST(COALESCE(c.size, LENGTH(c.deployment), 0) AS INTEGER) AS checkpoint_bytes
FROM updates AS u
LEFT JOIN checkpoints AS c ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE u.stack_id = ? AND u.id = ?;

-- name: SaveUpdate :execrows
-- A compare-and-swap: it changes nothing unless the stored revision is the
-- one the caller read.
UPDATE updates
SET status = @status, version = @version, revision = @revision,
    lease_digest = @lease_digest, lease_expires_at_ms = @lease_expires_at_ms,
    started_at_ms = @started_at_ms, ended_at_ms = @ended_at_ms,
    journal_version = @journal_version
WHERE stack_id = @stack_id AND id = @id
  AND revision = @expected_revision;

-- name: RunningMutatingUpdates :one
SELECT COUNT(*)
FROM updates
WHERE stack_id = ?
  AND status = 'running' AND dry_run = 0 AND kind <> 'preview';

-- name: HighestUpdateVersion :one
SELECT CAST(COALESCE(MAX(version), 0) AS INTEGER)
FROM updates
WHERE stack_id = ?
  AND status <> 'not started' AND dry_run = 0 AND kind <> 'preview';

-- name: GetStackVersion :one
SELECT version
FROM stacks
WHERE namespace = ? AND project = ? AND name = ? AND deleted_at_ms IS NULL;

-- name: SetStackVersion :exec
UPDATE stacks
SET version = ?, updated_at_ms = ?
WHERE namespace = ? AND project = ? AND name = ? AND deleted_at_ms IS NULL;

-- name: ActiveUpdate :one
SELECT u.stack_id, u.id, u.kind, u.dry_run, u.status, u.version, u.revision,
       u.lease_digest, u.lease_expires_at_ms, u.created_at_ms, u.started_at_ms, u.ended_at_ms,
       u.created_by, u.restored_from, u.journal_version,
       CAST(COALESCE(c.size, LENGTH(c.deployment), 0) AS INTEGER) AS checkpoint_bytes
FROM updates AS u
LEFT JOIN checkpoints AS c ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE u.stack_id = ?
  AND u.status = 'running' AND u.dry_run = 0 AND u.kind <> 'preview';

-- name: ListUpdates :many
SELECT u.stack_id, u.id, u.kind, u.dry_run, u.status, u.version, u.revision,
       u.lease_digest, u.lease_expires_at_ms, u.created_at_ms, u.started_at_ms, u.ended_at_ms,
       u.created_by, u.restored_from, u.journal_version,
       CAST(COALESCE(c.size, LENGTH(c.deployment), 0) AS INTEGER) AS checkpoint_bytes
FROM updates AS u
LEFT JOIN checkpoints AS c ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE u.stack_id = ?
  AND u.status <> 'not started' AND u.dry_run = 0 AND u.kind <> 'preview'
ORDER BY u.version DESC
LIMIT ? OFFSET ?;

-- name: GetUpdateLease :one
SELECT status, lease_digest
FROM updates
WHERE stack_id = ? AND id = ?;

-- name: GetUpdateKind :one
SELECT kind, dry_run
FROM updates
WHERE stack_id = ? AND id = ?;

-- name: GetLatestCheckpoint :one
SELECT c.stack_id, c.update_id, c.lease_digest, c.schema_version, c.sequence,
       c.digest, c.invalid, c.features_json, c.deployment, c.saved_at_ms, c.resource_count,
       c.blob_key, c.size, c.blob_size, c.blob_md5
FROM checkpoints AS c
JOIN updates AS u ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE c.stack_id = ?
ORDER BY u.version DESC
LIMIT 1;

-- name: GetCheckpointByUpdate :one
SELECT stack_id, update_id, lease_digest, schema_version, sequence,
       digest, invalid, features_json, deployment, saved_at_ms, resource_count,
       blob_key, size, blob_size, blob_md5
FROM checkpoints
WHERE stack_id = ? AND update_id = ?;

-- name: GetCheckpointByVersion :one
SELECT c.stack_id, c.update_id, c.lease_digest, c.schema_version, c.sequence,
       c.digest, c.invalid, c.features_json, c.deployment, c.saved_at_ms, c.resource_count,
       c.blob_key, c.size, c.blob_size, c.blob_md5
FROM checkpoints AS c
JOIN updates AS u ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE c.stack_id = ? AND u.version = ?;

-- name: PutCheckpoint :exec
INSERT INTO checkpoints (
    stack_id, update_id, lease_digest, schema_version, sequence,
    digest, invalid, features_json, deployment, saved_at_ms, resource_count,
    blob_key, size, blob_size, blob_md5
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (stack_id, update_id) DO UPDATE SET
    lease_digest = excluded.lease_digest,
    schema_version = excluded.schema_version,
    sequence = excluded.sequence,
    digest = excluded.digest,
    invalid = excluded.invalid,
    features_json = excluded.features_json,
    deployment = excluded.deployment,
    saved_at_ms = excluded.saved_at_ms,
    resource_count = excluded.resource_count,
    blob_key = excluded.blob_key,
    size = excluded.size,
    blob_size = excluded.blob_size,
    blob_md5 = excluded.blob_md5;

-- name: ListCheckpointBlobKeys :many
-- Every blob key the checkpoints of one stack name, without the empty ones,
-- ordered. A sweeper compares this set against the stored objects to find
-- blobs no checkpoint references.
SELECT blob_key
FROM checkpoints
WHERE stack_id = ? AND blob_key IS NOT NULL AND blob_key <> ''
ORDER BY blob_key;

-- name: LatestResourceCount :one
-- The stored managed resource count of the stack's latest checkpoint. It is
-- NULL when the stack has no checkpoint or when the checkpoint predates the
-- count: the caller then counts that one deployment. It reads only the count
-- column, never the deployment.
SELECT (
    SELECT c.resource_count
    FROM checkpoints AS c
    JOIN updates AS u ON c.stack_id = u.stack_id AND c.update_id = u.id
    WHERE c.stack_id = ?
    ORDER BY u.version DESC
    LIMIT 1
);

-- name: LatestDeployment :one
-- The deployment of the stack's latest checkpoint, for the delete guard of a
-- checkpoint that predates the stored count. It loads one deployment only.
SELECT c.deployment
FROM checkpoints AS c
JOIN updates AS u ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE c.stack_id = ?
ORDER BY u.version DESC
LIMIT 1;

-- name: SoftDeleteStack :execrows
-- Moves the name to deleted_name and frees it (ADR 47). A stack name cannot
-- hold ":", so "deleted:" and the ID never match a lookup by name.
UPDATE stacks
SET deleted_name = name, name = 'deleted:' || id, deleted_at_ms = ?
WHERE id = ? AND deleted_at_ms IS NULL;

-- name: DeleteStack :execrows
-- Not used since the soft delete (ADR 47); kept for the purge of a deleted stack.
DELETE FROM stacks
WHERE namespace = ? AND project = ? AND name = ?;

-- name: AppendEvent :exec
INSERT INTO update_events (stack_id, update_id, sequence, timestamp_ms, payload)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: ListEvents :many
SELECT sequence, timestamp_ms, payload
FROM update_events
WHERE stack_id = ? AND update_id = ? AND sequence > ?
ORDER BY sequence
LIMIT ?;

-- name: CreateSecretKey :execrows
INSERT INTO secret_keys (namespace, stack_id, wrapper_id, ciphertext, created_at_ms)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetSecretKey :one
SELECT namespace, stack_id, wrapper_id, ciphertext, created_at_ms
FROM secret_keys
WHERE namespace = ? AND stack_id = ?;

-- name: ListSecretWrapperIDs :many
SELECT DISTINCT wrapper_id
FROM secret_keys
ORDER BY wrapper_id;

-- name: DeleteSecretKeysOfStack :exec
DELETE FROM secret_keys
WHERE stack_id = ?;

-- name: UpsertInventory :exec
-- The lock columns stay out of the upsert: other writes keep the lock a
-- running update or a held workspace published, and only UpdateInventoryLock
-- changes it.
INSERT INTO inventory (namespace, tool, name, unit_id, display_name, last_changed_ms, status, resource_count)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (namespace, tool, name) DO UPDATE SET
    unit_id = excluded.unit_id,
    display_name = excluded.display_name,
    last_changed_ms = excluded.last_changed_ms,
    status = excluded.status,
    resource_count = excluded.resource_count;

-- name: UpdateInventoryLock :exec
-- Publishes a lock change of one unit: holder with take time and operation
-- to set it, all NULL to clear it. The caller writes it in the same
-- transaction as the lock itself.
UPDATE inventory
SET lock_holder = ?, lock_since_ms = ?, lock_operation = ?
WHERE namespace = ? AND tool = ? AND name = ?;

-- name: InsertDeletedInventory :exec
-- Records a deleted unit (ADR 47). The delete transaction removes the live
-- entry of the unit with DeleteInventory.
INSERT INTO inventory_deleted (namespace, tool, unit_id, name, deleted_at_ms, purge_after_ms)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ListDeletedInventory :many
SELECT namespace, tool, unit_id, name, deleted_at_ms, purge_after_ms
FROM inventory_deleted
WHERE namespace = ?
ORDER BY tool, name, unit_id;

-- name: DeleteInventory :exec
DELETE FROM inventory
WHERE namespace = ? AND tool = ? AND name = ?;

-- name: ListInventory :many
SELECT namespace, tool, name, unit_id, display_name, last_changed_ms, status, resource_count,
       lock_holder, lock_since_ms, lock_operation
FROM inventory
WHERE namespace = ?
ORDER BY tool, name;

-- name: CreateWorkspace :execrows
INSERT INTO workspaces (
    id, namespace, name, revision, lock_id, lock_holder, lock_reason,
    locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms,
    allow_forced_writes, allow_unlocked_writes
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetWorkspace :one
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes,
       deleted_at_ms, deleted_name
FROM workspaces
WHERE namespace = ? AND name = ? AND deleted_at_ms IS NULL;

-- name: GetWorkspaceByID :one
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes,
       deleted_at_ms, deleted_name
FROM workspaces
WHERE id = ? AND deleted_at_ms IS NULL;

-- name: ListWorkspaces :many
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes,
       deleted_at_ms, deleted_name
FROM workspaces
WHERE namespace = ? AND deleted_at_ms IS NULL
ORDER BY name;

-- name: SaveWorkspace :execrows
-- A compare-and-swap: it changes nothing unless the stored revision is the
-- one the caller read. The current state pointer stays: only FinalizeStateVersion moves it.
UPDATE workspaces
SET revision = @revision, lock_id = @lock_id, lock_holder = @lock_holder,
    lock_reason = @lock_reason, locked_at_ms = @locked_at_ms, updated_at_ms = @updated_at_ms,
    allow_forced_writes = @allow_forced_writes, allow_unlocked_writes = @allow_unlocked_writes
WHERE id = @id AND revision = @expected_revision;

-- name: SoftDeleteWorkspace :execrows
-- Moves the name to deleted_name and frees it (ADR 47). A workspace name
-- cannot hold ":", so "deleted:" and the ID never match a lookup by name. The
-- revision moves on, so a concurrent compare-and-swap of the row fails.
UPDATE workspaces
SET deleted_name = name, name = 'deleted:' || id, deleted_at_ms = ?, revision = revision + 1
WHERE id = ? AND deleted_at_ms IS NULL;

-- name: DeleteWorkspace :execrows
-- Not used since the soft delete (ADR 47); kept for the purge of a deleted workspace.
-- Removes one workspace. State versions go through DeleteStateVersionsOfWorkspace,
-- the data key through DeleteSecretKeysOfStack with the workspace ID.
DELETE FROM workspaces
WHERE namespace = ? AND name = ?;

-- name: DeleteStateVersionsOfWorkspace :exec
-- Removes every state version of one workspace, pending and finalized.
DELETE FROM state_versions
WHERE workspace_id = ?;

-- name: CreateStateVersion :exec
INSERT INTO state_versions (
    id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
    force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
    restored_from, sealed, sensitive_marks, fenced_by, unlocked_write
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetStateVersion :one
SELECT id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
       force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
       restored_from, sealed, sensitive_marks, fenced_by, unlocked_write
FROM state_versions
WHERE id = ?;

-- name: ListStateVersions :many
-- Finalized versions of one workspace, newest first. Ties on finalize time
-- break by ID, as the port comment promises. The caller normalizes the page.
SELECT id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
       force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
       restored_from, sealed, sensitive_marks, fenced_by, unlocked_write
FROM state_versions
WHERE workspace_id = ? AND status = 'finalized'
ORDER BY finalized_at_ms DESC, id ASC
LIMIT ? OFFSET ?;

-- name: CountStateVersions :one
SELECT COUNT(*)
FROM state_versions
WHERE workspace_id = ? AND status = 'finalized';

-- name: CountPendingStateVersions :one
SELECT COUNT(*)
FROM state_versions
WHERE workspace_id = ? AND status = 'pending';

-- name: ListStateContentKeys :many
-- Every blob key the versions of one workspace name, without the empty ones.
SELECT content_key
FROM state_versions
WHERE workspace_id = ? AND content_key <> '';

-- name: ListExpiredPendingStateVersions :many
-- Expired pending versions of one workspace, oldest expiry first. Ties on
-- expiry break by ID, as the port comment promises.
SELECT id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
       force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
       restored_from, sealed, sensitive_marks, fenced_by, unlocked_write
FROM state_versions
WHERE workspace_id = ? AND status = 'pending' AND upload_expires_at_ms < ?
ORDER BY upload_expires_at_ms ASC, id ASC;

-- name: DeleteStateVersion :execrows
-- Deletes one pending version. A finalized version matches no row, so a
-- concurrent finalize wins over a sweep: the sweep then skips the version.
DELETE FROM state_versions
WHERE id = ? AND status = 'pending';

-- name: SetPendingContentKey :execrows
-- Notes one upload attempt on a pending version. A finalized version
-- matches no row, so a late attempt reports no change.
UPDATE state_versions
SET content_key = ?
WHERE id = ? AND status = 'pending';

-- name: ListUnsealedStateVersions :many
-- Finalized versions that still hold plain content, ordered by ID, so the
-- seal job seals them in a stable order. A version without content stays
-- out: a pending row names no content yet.
SELECT id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
       force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
       restored_from, sealed, sensitive_marks, fenced_by, unlocked_write
FROM state_versions
WHERE sealed = 0 AND status = 'finalized' AND content_key <> ''
ORDER BY id ASC;

-- name: SealStateVersion :execrows
-- Moves one unsealed version to its sealed blob with its sealed outputs and
-- marks it sealed. The write matches only an unsealed row, so a retry after
-- a crash succeeds and changes nothing.
UPDATE state_versions
SET content_key = @content_key, outputs = @outputs, sealed = 1
WHERE id = @id AND sealed = 0;

-- name: SaveSensitiveMarks :execrows
-- Stores the secret paths of the json-state rendering of one version. It
-- works on a pending and on a finalized version: the rendering may land
-- after the state upload finalizes. A missing version matches no row.
UPDATE state_versions
SET sensitive_marks = ?
WHERE id = ?;

-- name: FinalizeStateVersion :execrows
-- Marks one pending version finalized with its recorded attempt. The write
-- matches only the recorded content key, so a superseded attempt reports no
-- change instead of finalizing over a newer one; a finalized version also
-- matches no row, so a retry reports no change. It also sets the declared
-- serial, lineage, MD5 and outputs: a streamed write declares them only after
-- its content is read (ADR 49, decision 7), and its row holds placeholders
-- until then.
UPDATE state_versions
SET serial = @serial, lineage = @lineage, md5 = @md5, outputs = @outputs, status = @status, finalized_at_ms = @finalized_at_ms, size = @size
WHERE id = @id AND status = 'pending' AND content_key = @content_key;

-- name: SetWorkspaceCurrent :exec
UPDATE workspaces
SET current_state_version_id = ?, updated_at_ms = ?
WHERE id = ?;

-- name: GetCurrentStateVersion :one
-- Follows the workspace pointer: a forced upload may carry a lower serial and
-- still be current, so the highest serial is not always the current state.
SELECT sv.id, sv.workspace_id, sv.status, sv.serial, sv.lineage, sv.md5, sv.size,
       sv.content_key, sv.outputs, sv.force, sv.upload_digest, sv.upload_expires_at_ms,
       sv.holder, sv.lock_id, sv.created_at_ms, sv.finalized_at_ms, sv.restored_from, sv.sealed,
       sv.sensitive_marks, sv.fenced_by, unlocked_write
FROM state_versions AS sv
JOIN workspaces AS w ON w.current_state_version_id = sv.id
WHERE w.id = ?;

-- name: CreateBackup :execrows
INSERT INTO backups (id, state, started_at_ms, ended_at_ms, target, error)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetBackup :one
SELECT id, state, started_at_ms, ended_at_ms, target, error
FROM backups
WHERE id = ?;

-- name: ListBackups :many
SELECT id, state, started_at_ms, ended_at_ms, target, error
FROM backups
ORDER BY started_at_ms DESC, id ASC;

-- name: FinishBackup :execrows
-- Moves one running backup to its end state. A finished backup matches no
-- row, so a retry reports no change.
UPDATE backups
SET state = ?, error = ?, ended_at_ms = ?
WHERE id = ? AND state = 'running';

-- name: FailRunningBackups :exec
-- Marks every running backup failed. The server calls it at start, after a
-- stop during a backup.
UPDATE backups
SET state = 'failed', error = ?, ended_at_ms = ?
WHERE state = 'running';

-- name: GetInstallationState :one
-- The installation state (ADR 38 decision 4): normal or maintenance. The
-- migration seeds the one row; a missing row reads normal.
SELECT state
FROM installation
WHERE id = 1;

-- name: UpsertInstallationState :exec
-- Stores the installation state, creating the row when it is missing.
INSERT INTO installation (id, state)
VALUES (1, ?)
ON CONFLICT (id) DO UPDATE SET state = excluded.state;

-- name: SetInstallationStateIf :execrows
-- Stores next only when the stored state is expected: a compare-and-swap, so
-- enters on several instances of one database stay ordered. It changes
-- nothing when the stored state differs or the row is missing.
UPDATE installation
SET state = ?
WHERE id = 1 AND state = ?;

-- name: CreateRestore :execrows
INSERT INTO restores (id, state, backup_id, pre_backup_id, requested_by, started_at_ms, ended_at_ms, error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetRestore :one
SELECT id, state, backup_id, pre_backup_id, requested_by, started_at_ms, ended_at_ms, error
FROM restores
WHERE id = ?;

-- name: ListRestores :many
SELECT id, state, backup_id, pre_backup_id, requested_by, started_at_ms, ended_at_ms, error
FROM restores
ORDER BY started_at_ms DESC, id ASC;

-- name: FinishRestore :execrows
-- Moves one running restore to its end state. A finished restore matches no
-- row, so a retry reports no change.
UPDATE restores
SET state = ?, error = ?, ended_at_ms = ?
WHERE id = ? AND state IN ('checking', 'applying');

-- name: MarkRestoreApplying :execrows
-- Moves one checking restore to applying with the backup taken before the
-- apply. Any other state matches no row, so a retry reports no change.
UPDATE restores
SET state = 'applying', pre_backup_id = ?
WHERE id = ? AND state = 'checking';

-- name: FailRunningRestores :exec
-- Marks every running restore failed. The server calls it at start, after a
-- stop during a restore.
UPDATE restores
SET state = 'failed', error = ?, ended_at_ms = ?
WHERE state IN ('checking', 'applying');

-- name: BumpFailoverVersions :execrows
-- Increments the failover version of every namespace by one (ADR 38
-- decision 7), so leases and locks from before the restore stop working.
UPDATE namespaces
SET failover_version = failover_version + 1;

-- name: LastAuditEntry :one
-- The entry with the highest sequence of a namespace, for the next append.
-- A namespace with no entries matches no row.
SELECT namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
       at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count
FROM audit_entries
WHERE namespace = ?
ORDER BY sequence DESC
LIMIT 1;

-- name: InsertAuditEntry :exec
INSERT INTO audit_entries (
    namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
    at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAuditEntries :many
-- One page of the entries of a namespace, newest first. Ties on time break
-- by sequence, as the port comment promises.
SELECT namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
       at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count
FROM audit_entries
WHERE namespace = ?
ORDER BY sequence DESC
LIMIT ? OFFSET ?;

-- name: CountAuditEntries :one
SELECT COUNT(*)
FROM audit_entries
WHERE namespace = ?;

-- name: ListAuditChainPage :many
-- One bounded page of the chain of a namespace oldest first, for the chain
-- check. The check reads the chain with this query in pages of
-- audit.CheckPageSize entries after the last sequence it saw, so a namespace
-- with a large log never grows the check without a bound.
SELECT namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
       at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count
FROM audit_entries
WHERE namespace = ? AND sequence > ?
ORDER BY sequence ASC
LIMIT ?;

-- name: JournalSequenceSHA :one
SELECT sha256 FROM update_journal_sequences
WHERE stack_id = ? AND update_id = ? AND sequence = ?;

-- name: NextJournalArrival :one
SELECT CAST(COALESCE(MAX(arrival), 0) + 1 AS INTEGER) AS arrival
FROM update_journal
WHERE stack_id = ? AND update_id = ?;

-- name: InsertJournal :exec
INSERT INTO update_journal (
    stack_id, update_id, arrival, blob_key, sha256, md5, size,
    lowest_sequence, sequence_ids, operation_ids
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: InsertJournalSequence :exec
INSERT INTO update_journal_sequences (stack_id, update_id, sequence, arrival, sha256)
VALUES (?, ?, ?, ?, ?);

-- name: ListJournalOperations :many
SELECT operation_ids FROM update_journal
WHERE stack_id = ? AND update_id = ?
ORDER BY arrival;

-- name: JournalBlobKeys :many
SELECT blob_key FROM update_journal
WHERE stack_id = ? AND update_id = ?
ORDER BY arrival;

-- name: DeleteJournalSequences :exec
DELETE FROM update_journal_sequences
WHERE stack_id = ? AND update_id = ?;

-- name: DeleteJournal :exec
DELETE FROM update_journal
WHERE stack_id = ? AND update_id = ?;

-- name: JournalState :one
SELECT journal_closed, status FROM updates
WHERE stack_id = ? AND id = ?;

-- name: CloseJournal :exec
UPDATE updates SET journal_closed = 1
WHERE stack_id = ? AND id = ?;

-- name: CreatePrincipal :execrows
INSERT INTO principals (id, subject, display_name, kind, source, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetPrincipal :one
SELECT p.id, p.subject, p.display_name, p.kind, p.source, p.created_at_ms, p.updated_at_ms,
  (SELECT COUNT(*) FROM group_members m WHERE m.principal_id = p.id AND m.removed_at_ms IS NULL) AS group_count
FROM principals p WHERE p.id = ? AND p.deleted_at_ms IS NULL;

-- name: GetPrincipalBySubject :one
SELECT p.id, p.subject, p.display_name, p.kind, p.source, p.created_at_ms, p.updated_at_ms,
  (SELECT COUNT(*) FROM group_members m WHERE m.principal_id = p.id AND m.removed_at_ms IS NULL) AS group_count
FROM principals p WHERE p.subject = sqlc.arg('subject') AND p.deleted_at_ms IS NULL;

-- name: ListPrincipals :many
SELECT p.id, p.subject, p.display_name, p.kind, p.source, p.created_at_ms, p.updated_at_ms,
  (SELECT COUNT(*) FROM group_members m WHERE m.principal_id = p.id AND m.removed_at_ms IS NULL) AS group_count
FROM principals p
WHERE p.deleted_at_ms IS NULL
  AND p.subject > sqlc.arg('after')
  AND p.subject >= sqlc.arg('low')
  AND (CAST(sqlc.arg('high') AS TEXT) = '' OR p.subject < sqlc.arg('high'))
  AND (CAST(sqlc.arg('kind') AS TEXT) = '' OR p.kind = sqlc.arg('kind'))
ORDER BY p.subject
LIMIT sqlc.arg('row_limit');

-- name: CountPrincipals :one
SELECT COUNT(*) FROM principals
WHERE deleted_at_ms IS NULL
  AND subject >= sqlc.arg('low')
  AND (CAST(sqlc.arg('high') AS TEXT) = '' OR subject < sqlc.arg('high'))
  AND (CAST(sqlc.arg('kind') AS TEXT) = '' OR kind = sqlc.arg('kind'));

-- name: UpdatePrincipal :execrows
UPDATE principals SET display_name = ?, updated_at_ms = ?
WHERE id = ? AND deleted_at_ms IS NULL;

-- name: DeletePrincipal :execrows
UPDATE principals SET deleted_at_ms = ?
WHERE id = ? AND deleted_at_ms IS NULL;

-- name: EndMembershipsOfPrincipal :exec
UPDATE group_members SET removed_at_ms = ?
WHERE principal_id = ? AND removed_at_ms IS NULL;

-- name: CreateGroup :execrows
INSERT INTO principal_groups (id, name, name_key, description, source, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetGroup :one
SELECT g.id, g.name, g.description, g.source, g.created_at_ms, g.updated_at_ms,
  (SELECT COUNT(*) FROM group_members m WHERE m.group_id = g.id AND m.removed_at_ms IS NULL) AS member_count
FROM principal_groups g WHERE g.id = ? AND g.deleted_at_ms IS NULL;

-- name: GroupIDByNameKey :one
SELECT id FROM principal_groups WHERE name_key = sqlc.arg('name_key') AND deleted_at_ms IS NULL;

-- name: ListGroups :many
SELECT g.id, g.name, g.description, g.source, g.created_at_ms, g.updated_at_ms,
  (SELECT COUNT(*) FROM group_members m WHERE m.group_id = g.id AND m.removed_at_ms IS NULL) AS member_count
FROM principal_groups g
WHERE g.deleted_at_ms IS NULL
  AND g.name_key > sqlc.arg('after')
  AND g.name_key >= sqlc.arg('low')
  AND (CAST(sqlc.arg('high') AS TEXT) = '' OR g.name_key < sqlc.arg('high'))
ORDER BY g.name_key
LIMIT sqlc.arg('row_limit');

-- name: CountGroups :one
SELECT COUNT(*) FROM principal_groups
WHERE deleted_at_ms IS NULL
  AND name_key >= sqlc.arg('low')
  AND (CAST(sqlc.arg('high') AS TEXT) = '' OR name_key < sqlc.arg('high'));

-- name: UpdateGroup :execrows
UPDATE principal_groups SET name = ?, name_key = ?, description = ?, updated_at_ms = ?
WHERE id = ? AND deleted_at_ms IS NULL;

-- name: DeleteGroup :execrows
UPDATE principal_groups SET deleted_at_ms = ?
WHERE id = ? AND deleted_at_ms IS NULL;

-- name: EndMembershipsInGroup :exec
UPDATE group_members SET removed_at_ms = ?
WHERE group_id = ? AND removed_at_ms IS NULL;

-- name: PrincipalExists :one
SELECT COUNT(*) FROM principals WHERE id = ? AND deleted_at_ms IS NULL;

-- name: GroupSource :one
SELECT source FROM principal_groups WHERE id = ? AND deleted_at_ms IS NULL;

-- name: AddGroupMember :execrows
INSERT INTO group_members (group_id, principal_id, added_at_ms)
VALUES (?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: RemoveGroupMember :execrows
UPDATE group_members SET removed_at_ms = ?
WHERE group_id = ? AND principal_id = ? AND removed_at_ms IS NULL;

-- name: ListGroupMembers :many
SELECT p.id, p.subject, p.display_name, p.kind, p.source, p.created_at_ms, p.updated_at_ms,
  (SELECT COUNT(*) FROM group_members o WHERE o.principal_id = p.id AND o.removed_at_ms IS NULL) AS group_count,
  m.added_at_ms
FROM group_members m JOIN principals p ON p.id = m.principal_id
WHERE m.group_id = sqlc.arg('group_id') AND m.removed_at_ms IS NULL
  AND p.subject > sqlc.arg('after')
  AND p.subject >= sqlc.arg('low')
  AND (CAST(sqlc.arg('high') AS TEXT) = '' OR p.subject < sqlc.arg('high'))
ORDER BY p.subject
LIMIT sqlc.arg('row_limit');

-- name: CountGroupMembers :one
SELECT COUNT(*)
FROM group_members m JOIN principals p ON p.id = m.principal_id
WHERE m.group_id = sqlc.arg('group_id') AND m.removed_at_ms IS NULL
  AND p.subject >= sqlc.arg('low')
  AND (CAST(sqlc.arg('high') AS TEXT) = '' OR p.subject < sqlc.arg('high'));

-- name: ListGroupsOfPrincipal :many
SELECT g.id, g.name, g.description, g.source, g.created_at_ms, g.updated_at_ms,
  (SELECT COUNT(*) FROM group_members o WHERE o.group_id = g.id AND o.removed_at_ms IS NULL) AS member_count
FROM group_members m JOIN principal_groups g ON g.id = m.group_id
WHERE m.principal_id = sqlc.arg('principal_id') AND m.removed_at_ms IS NULL
  AND g.name_key > sqlc.arg('after')
  AND g.name_key >= sqlc.arg('low')
  AND (CAST(sqlc.arg('high') AS TEXT) = '' OR g.name_key < sqlc.arg('high'))
ORDER BY g.name_key
LIMIT sqlc.arg('row_limit');

-- name: CountGroupsOfPrincipal :one
SELECT COUNT(*)
FROM group_members m JOIN principal_groups g ON g.id = m.group_id
WHERE m.principal_id = sqlc.arg('principal_id') AND m.removed_at_ms IS NULL
  AND g.name_key >= sqlc.arg('low')
  AND (CAST(sqlc.arg('high') AS TEXT) = '' OR g.name_key < sqlc.arg('high'));

-- name: CreateRole :execrows
INSERT INTO roles (id, name, name_key, description, actions, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetRole :one
SELECT id, name, description, actions, created_at_ms, updated_at_ms FROM roles
WHERE id = ? AND deleted_at_ms IS NULL;

-- name: RolesByIDs :many
SELECT id, name, description, actions, created_at_ms, updated_at_ms FROM roles
WHERE id IN (sqlc.slice('ids')) AND deleted_at_ms IS NULL;

-- name: RoleIDByNameKey :one
SELECT id FROM roles WHERE name_key = ? AND deleted_at_ms IS NULL;

-- name: ListRoles :many
SELECT id, name, description, actions, created_at_ms, updated_at_ms FROM roles
WHERE deleted_at_ms IS NULL
ORDER BY name_key;

-- name: UpdateRole :execrows
UPDATE roles SET name = ?, name_key = ?, description = ?, actions = ?, updated_at_ms = ?
WHERE id = ? AND deleted_at_ms IS NULL;

-- name: DeleteRole :execrows
UPDATE roles SET deleted_at_ms = ? WHERE id = ? AND deleted_at_ms IS NULL;

-- name: CountGrantsWithRole :one
SELECT COUNT(*) FROM grants WHERE role = ? AND revoked_at_ms IS NULL;

-- name: CreateGrant :execrows
INSERT INTO grants (id, subject, subject_kind, subject_id, role, object, target, object_kind, namespace, condition, created_at_ms, created_by)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetGrant :one
SELECT id, subject, subject_kind, subject_id, role, object, target, object_kind, namespace, condition, created_at_ms, created_by
FROM grants WHERE id = ? AND revoked_at_ms IS NULL;

-- name: ListGrantsOn :many
SELECT id, subject, subject_kind, subject_id, role, object, target, object_kind, namespace, condition, created_at_ms, created_by
FROM grants
WHERE target = sqlc.arg('target') AND revoked_at_ms IS NULL
  AND (subject > sqlc.arg('after_subject') OR (subject = sqlc.arg('after_subject') AND role > sqlc.arg('after_role')))
ORDER BY subject, role
LIMIT sqlc.arg('row_limit');

-- name: CountGrantsOn :one
SELECT COUNT(*) FROM grants WHERE target = ? AND revoked_at_ms IS NULL;

-- name: RevokeGrant :execrows
UPDATE grants SET revoked_at_ms = ?, revoked_by = ? WHERE id = ? AND revoked_at_ms IS NULL;

-- name: GrantsOn :many
SELECT id, subject, subject_kind, subject_id, role, object, target, object_kind, namespace, condition, created_at_ms, created_by
FROM grants
WHERE subject IN (sqlc.slice('subjects')) AND target IN (sqlc.slice('targets')) AND revoked_at_ms IS NULL
ORDER BY target, subject, role;

-- name: GrantsWithRoles :many
SELECT id, subject, subject_kind, subject_id, role, object, target, object_kind, namespace, condition, created_at_ms, created_by
FROM grants
WHERE subject IN (sqlc.slice('subjects')) AND role IN (sqlc.slice('roles')) AND revoked_at_ms IS NULL
ORDER BY target, subject, role;

-- name: RevokeGrantsOfSubject :exec
UPDATE grants SET revoked_at_ms = ?, revoked_by = ?
WHERE subject_kind = ? AND subject_id = ? AND revoked_at_ms IS NULL;

-- name: GrantsOfSubject :many
SELECT id, subject, subject_kind, subject_id, role, object, target, object_kind, namespace, condition, created_at_ms, created_by
FROM grants
WHERE subject_kind = ? AND subject_id = ? AND revoked_at_ms IS NULL
ORDER BY target, role;

-- name: CountRoleGrantsBelowInstallation :one
SELECT COUNT(*) FROM grants WHERE role = ? AND object_kind <> 'installation' AND revoked_at_ms IS NULL;

-- name: CountEndedGrantRecords :one
-- The records of a new grant that have ended: its principal or group, and
-- its custom role. The grant write checks it in its own transaction, so a
-- delete that commits first wins.
SELECT (SELECT COUNT(*) FROM principals p WHERE CAST(sqlc.arg('subject_kind') AS TEXT) = 'principal'
          AND p.id = CAST(sqlc.arg('subject_id') AS TEXT) AND p.deleted_at_ms IS NOT NULL)
     + (SELECT COUNT(*) FROM principal_groups g WHERE CAST(sqlc.arg('subject_kind') AS TEXT) = 'group'
          AND g.id = CAST(sqlc.arg('subject_id') AS TEXT) AND g.deleted_at_ms IS NOT NULL)
     + (SELECT COUNT(*) FROM roles r WHERE r.id = CAST(sqlc.arg('role') AS TEXT) AND r.deleted_at_ms IS NOT NULL);

-- name: SubjectsOfCaller :many
SELECT p.id AS principal_id, CAST(COALESCE(m.group_id, '') AS TEXT) AS group_id
FROM principals p
LEFT JOIN group_members m ON m.principal_id = p.id AND m.removed_at_ms IS NULL
WHERE p.subject = ? AND p.deleted_at_ms IS NULL;

-- name: ReviewGrants :many
-- One page of an access-review export (ADR 52, decision 10). The join on
-- group_members reads the memberships of the groups of the grants: of one
-- principal for a subject filter, or of every member for expand. It gives
-- one row for each membership that overlaps the grant and the window (a
-- history), or that is current at as_of_ms (a snapshot). In a history, a
-- grant on a container with a custom role is in the export, a deleted role
-- included. A JSON array carries each list, so the query stays fixed.
SELECT g.id, g.subject, g.subject_kind, g.subject_id, g.role, g.object, g.target, g.condition,
  g.created_at_ms, g.created_by, g.revoked_at_ms, g.revoked_by,
  CAST(COALESCE(m.added_at_ms, 0) AS INTEGER) AS member_added_at_ms,
  m.removed_at_ms AS member_removed_at_ms,
  CAST(COALESCE(m.principal_id, '') AS TEXT) AS member_id,
  CAST(COALESCE(mp.subject, '') AS TEXT) AS member_subject,
  CAST(COALESCE(p.subject, pg.name, '') AS TEXT) AS subject_name,
  (SELECT COUNT(*) FROM group_members c
   WHERE g.subject_kind = 'group' AND c.group_id = g.subject_id
     AND c.added_at_ms <= CAST(sqlc.arg('as_of_ms') AS INTEGER)
     AND COALESCE(c.removed_at_ms, 9223372036854775807) > CAST(sqlc.arg('as_of_ms') AS INTEGER)) AS member_count
FROM grants g
LEFT JOIN principals p ON g.subject_kind = 'principal' AND p.id = g.subject_id
LEFT JOIN principal_groups pg ON g.subject_kind = 'group' AND pg.id = g.subject_id
LEFT JOIN group_members m ON g.subject_kind = 'group' AND m.group_id = g.subject_id
  AND ((CAST(sqlc.arg('principal') AS TEXT) <> '' AND m.principal_id = CAST(sqlc.arg('principal') AS TEXT))
    OR (CAST(sqlc.arg('principal') AS TEXT) = '' AND CAST(sqlc.arg('expand') AS BOOLEAN) = 1))
  AND CASE WHEN CAST(sqlc.arg('history') AS BOOLEAN) = 1 THEN
    m.added_at_ms < CAST(sqlc.arg('to_ms') AS INTEGER)
    AND COALESCE(m.removed_at_ms, 9223372036854775807) > CAST(sqlc.arg('from_ms') AS INTEGER)
    AND m.added_at_ms < COALESCE(g.revoked_at_ms, 9223372036854775807)
    AND COALESCE(m.removed_at_ms, 9223372036854775807) > g.created_at_ms
  ELSE m.added_at_ms <= CAST(sqlc.arg('as_of_ms') AS INTEGER) AND COALESCE(m.removed_at_ms, 9223372036854775807) > CAST(sqlc.arg('as_of_ms') AS INTEGER) END
LEFT JOIN principals mp ON mp.id = m.principal_id
WHERE (CAST(sqlc.arg('all_grants') AS BOOLEAN) = 1
    OR (CAST(sqlc.arg('namespace') AS TEXT) <> '' AND g.namespace = CAST(sqlc.arg('namespace') AS TEXT)
      AND g.object_kind <> 'installation')
    OR g.target = CAST(sqlc.arg('target') AS TEXT)
    OR (g.target IN (SELECT value FROM json_each(CAST(sqlc.arg('containers') AS TEXT)))
      AND (g.role IN (SELECT value FROM json_each(CAST(sqlc.arg('container_roles') AS TEXT)))
        OR (CAST(sqlc.arg('history') AS BOOLEAN) = 1 AND EXISTS (SELECT 1 FROM roles r WHERE r.id = g.role)))))
  AND (CAST(sqlc.arg('subject') AS TEXT) = '' OR g.subject = CAST(sqlc.arg('subject') AS TEXT)
    OR (CAST(sqlc.arg('principal') AS TEXT) <> '' AND m.principal_id IS NOT NULL))
  AND CASE WHEN CAST(sqlc.arg('history') AS BOOLEAN) = 1 THEN
    g.created_at_ms < CAST(sqlc.arg('to_ms') AS INTEGER)
    AND COALESCE(g.revoked_at_ms, 9223372036854775807) > CAST(sqlc.arg('from_ms') AS INTEGER)
  ELSE g.created_at_ms <= CAST(sqlc.arg('as_of_ms') AS INTEGER) AND COALESCE(g.revoked_at_ms, 9223372036854775807) > CAST(sqlc.arg('as_of_ms') AS INTEGER) END
  AND (g.created_at_ms > CAST(sqlc.arg('after_at_ms') AS INTEGER)
    OR (g.created_at_ms = CAST(sqlc.arg('after_at_ms') AS INTEGER)
      AND (g.id > CAST(sqlc.arg('after_id') AS TEXT)
        OR (g.id = CAST(sqlc.arg('after_id') AS TEXT)
          AND (COALESCE(m.added_at_ms, 0) > CAST(sqlc.arg('after_member_at_ms') AS INTEGER)
            OR (COALESCE(m.added_at_ms, 0) = CAST(sqlc.arg('after_member_at_ms') AS INTEGER)
              AND COALESCE(m.principal_id, '') > CAST(sqlc.arg('after_member_id') AS TEXT)))))))
ORDER BY g.created_at_ms, g.id, member_added_at_ms, member_id
LIMIT sqlc.arg('row_limit');

-- name: CountReviewGrants :one
SELECT COUNT(*)
FROM grants g
LEFT JOIN group_members m ON g.subject_kind = 'group' AND m.group_id = g.subject_id
  AND ((CAST(sqlc.arg('principal') AS TEXT) <> '' AND m.principal_id = CAST(sqlc.arg('principal') AS TEXT))
    OR (CAST(sqlc.arg('principal') AS TEXT) = '' AND CAST(sqlc.arg('expand') AS BOOLEAN) = 1))
  AND CASE WHEN CAST(sqlc.arg('history') AS BOOLEAN) = 1 THEN
    m.added_at_ms < CAST(sqlc.arg('to_ms') AS INTEGER)
    AND COALESCE(m.removed_at_ms, 9223372036854775807) > CAST(sqlc.arg('from_ms') AS INTEGER)
    AND m.added_at_ms < COALESCE(g.revoked_at_ms, 9223372036854775807)
    AND COALESCE(m.removed_at_ms, 9223372036854775807) > g.created_at_ms
  ELSE m.added_at_ms <= CAST(sqlc.arg('as_of_ms') AS INTEGER) AND COALESCE(m.removed_at_ms, 9223372036854775807) > CAST(sqlc.arg('as_of_ms') AS INTEGER) END
WHERE (CAST(sqlc.arg('all_grants') AS BOOLEAN) = 1
    OR (CAST(sqlc.arg('namespace') AS TEXT) <> '' AND g.namespace = CAST(sqlc.arg('namespace') AS TEXT)
      AND g.object_kind <> 'installation')
    OR g.target = CAST(sqlc.arg('target') AS TEXT)
    OR (g.target IN (SELECT value FROM json_each(CAST(sqlc.arg('containers') AS TEXT)))
      AND (g.role IN (SELECT value FROM json_each(CAST(sqlc.arg('container_roles') AS TEXT)))
        OR (CAST(sqlc.arg('history') AS BOOLEAN) = 1 AND EXISTS (SELECT 1 FROM roles r WHERE r.id = g.role)))))
  AND (CAST(sqlc.arg('subject') AS TEXT) = '' OR g.subject = CAST(sqlc.arg('subject') AS TEXT)
    OR (CAST(sqlc.arg('principal') AS TEXT) <> '' AND m.principal_id IS NOT NULL))
  AND CASE WHEN CAST(sqlc.arg('history') AS BOOLEAN) = 1 THEN
    g.created_at_ms < CAST(sqlc.arg('to_ms') AS INTEGER)
    AND COALESCE(g.revoked_at_ms, 9223372036854775807) > CAST(sqlc.arg('from_ms') AS INTEGER)
  ELSE g.created_at_ms <= CAST(sqlc.arg('as_of_ms') AS INTEGER) AND COALESCE(g.revoked_at_ms, 9223372036854775807) > CAST(sqlc.arg('as_of_ms') AS INTEGER) END;

-- name: ReviewStacksByIDs :many
-- The stacks of the unit grants of an export, deleted ones included (ADR 47):
-- an export shows a grant on a deleted unit, with its name at the time of
-- the delete, so a reviewer can clean it up.
SELECT id, namespace, project, CAST(COALESCE(deleted_name, name) AS TEXT) AS name, deleted_at_ms FROM stacks WHERE id IN (sqlc.slice('ids'));

-- name: ReviewWorkspacesByIDs :many
-- The workspaces of the unit grants of an export, deleted ones included.
SELECT id, namespace, CAST(COALESCE(deleted_name, name) AS TEXT) AS name, deleted_at_ms FROM workspaces WHERE id IN (sqlc.slice('ids'));

-- name: UnitStackID :one
-- The stable ID of a Pulumi stack, for a grant on the unit (ADR 19, ADR 52).
SELECT id FROM stacks WHERE namespace = ? AND project = ? AND name = ? AND deleted_at_ms IS NULL;

-- name: UnitWorkspaceID :one
-- The stable ID of an OpenTofu workspace, for a grant on the unit.
SELECT id FROM workspaces WHERE namespace = ? AND name = ? AND deleted_at_ms IS NULL;

-- name: UnitStacksByIDs :many
-- A deleted stack (ADR 47) is left out: its grants give nothing until a
-- restore brings it back.
SELECT id, namespace, project, name FROM stacks WHERE id IN (sqlc.slice('ids')) AND deleted_at_ms IS NULL;

-- name: UnitWorkspacesByIDs :many
-- A deleted workspace (ADR 47) is left out: its grants give nothing until
-- a restore brings it back.
SELECT id, namespace, name FROM workspaces WHERE id IN (sqlc.slice('ids')) AND deleted_at_ms IS NULL;

-- Access tokens (ADR 53).

-- name: CreateAccessToken :execrows
INSERT INTO access_tokens (id, principal_id, name, kind, digest, created_at_ms, expires_at_ms, created_by)
SELECT sqlc.arg('id'), p.id, sqlc.arg('name'), sqlc.arg('kind'), sqlc.arg('digest'),
  sqlc.arg('created_at_ms'), sqlc.arg('expires_at_ms'), sqlc.arg('created_by')
FROM principals p
WHERE p.id = sqlc.arg('principal_id') AND p.deleted_at_ms IS NULL
ON CONFLICT DO NOTHING;

-- name: CountLivePrincipal :one
SELECT COUNT(*) FROM principals WHERE id = ? AND deleted_at_ms IS NULL;

-- name: CountAccessToken :one
SELECT COUNT(*) FROM access_tokens WHERE id = ?;

-- name: GetAccessToken :one
SELECT id, principal_id, name, kind, created_at_ms, expires_at_ms, last_used_at_ms, revoked_at_ms, replaced_by, created_by
FROM access_tokens WHERE id = ?;

-- name: GetAccessTokenByDigest :one
SELECT t.id, t.principal_id, t.name, t.kind, t.digest, t.created_at_ms, t.expires_at_ms, t.last_used_at_ms,
  t.revoked_at_ms, t.replaced_by, t.created_by, p.subject, p.display_name, p.kind AS principal_kind
FROM access_tokens t
JOIN principals p ON p.id = t.principal_id
WHERE t.digest = ? AND p.deleted_at_ms IS NULL;

-- name: ListAccessTokens :many
SELECT id, principal_id, name, kind, created_at_ms, expires_at_ms, last_used_at_ms, revoked_at_ms, replaced_by, created_by
FROM access_tokens
WHERE (CAST(sqlc.arg('principal_id') AS TEXT) = '' OR principal_id = sqlc.arg('principal_id'))
  AND (CAST(sqlc.arg('status') AS INTEGER) = 0
    OR ((CAST(sqlc.arg('status') AS INTEGER) = 1 OR CAST(sqlc.arg('status') AS INTEGER) = 2) AND revoked_at_ms IS NULL AND expires_at_ms > CAST(sqlc.arg('at_ms') AS INTEGER)
      AND (CAST(sqlc.arg('status') AS INTEGER) = 1 OR expires_at_ms <= CAST(sqlc.arg('soon_ms') AS INTEGER)))
    OR (CAST(sqlc.arg('status') AS INTEGER) = 3 AND (revoked_at_ms IS NOT NULL OR expires_at_ms <= CAST(sqlc.arg('at_ms') AS INTEGER))))
  AND (CAST(sqlc.arg('has_after') AS INTEGER) = 0
    OR created_at_ms < sqlc.arg('after_ms')
    OR (created_at_ms = sqlc.arg('after_ms') AND id < sqlc.arg('after_id')))
ORDER BY created_at_ms DESC, id DESC
LIMIT sqlc.arg('row_limit');

-- name: CountAccessTokens :one
SELECT COUNT(*) FROM access_tokens
WHERE (CAST(sqlc.arg('principal_id') AS TEXT) = '' OR principal_id = sqlc.arg('principal_id'))
  AND (CAST(sqlc.arg('status') AS INTEGER) = 0
    OR ((CAST(sqlc.arg('status') AS INTEGER) = 1 OR CAST(sqlc.arg('status') AS INTEGER) = 2) AND revoked_at_ms IS NULL AND expires_at_ms > CAST(sqlc.arg('at_ms') AS INTEGER)
      AND (CAST(sqlc.arg('status') AS INTEGER) = 1 OR expires_at_ms <= CAST(sqlc.arg('soon_ms') AS INTEGER)))
    OR (CAST(sqlc.arg('status') AS INTEGER) = 3 AND (revoked_at_ms IS NOT NULL OR expires_at_ms <= CAST(sqlc.arg('at_ms') AS INTEGER))));

-- name: RevokeAccessToken :execrows
UPDATE access_tokens SET revoked_at_ms = CAST(sqlc.arg('revoked_at_ms') AS INTEGER)
WHERE id = sqlc.arg('id') AND revoked_at_ms IS NULL;

-- name: EndRotatedAccessToken :execrows
UPDATE access_tokens
SET expires_at_ms = MIN(expires_at_ms, CAST(sqlc.arg('ends_at_ms') AS INTEGER)),
  replaced_by = CAST(sqlc.arg('replaced_by') AS TEXT)
WHERE id = sqlc.arg('id') AND revoked_at_ms IS NULL AND replaced_by IS NULL
  AND expires_at_ms > sqlc.arg('now_ms');

-- name: TouchAccessToken :exec
UPDATE access_tokens SET last_used_at_ms = CAST(sqlc.arg('at_ms') AS INTEGER)
WHERE id = sqlc.arg('id')
  AND (last_used_at_ms IS NULL OR last_used_at_ms <= CAST(sqlc.arg('before_ms') AS INTEGER));
