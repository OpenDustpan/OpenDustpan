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
WHERE namespace = ? AND project = ? AND name = ?;

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
UPDATE stacks SET name = ? WHERE namespace = ? AND project = ? AND name = ?;

-- name: GetStackID :one
SELECT id
FROM stacks
WHERE namespace = ? AND project = ? AND name = ?;

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
WHERE namespace = ? AND project = ? AND name = ?;

-- name: SetStackVersion :exec
UPDATE stacks
SET version = ?, updated_at_ms = ?
WHERE namespace = ? AND project = ? AND name = ?;

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
WHERE namespace = ? AND name = ?;

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
