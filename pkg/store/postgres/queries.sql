-- name: CreateNamespace :execrows
INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING;

-- name: GetNamespace :one
SELECT name, home_region, failover_version, created_at_ms, allow_forced_writes, allow_unlocked_writes
FROM namespaces
WHERE name = $1;

-- name: ListNamespaces :many
SELECT name, home_region, failover_version, created_at_ms, allow_forced_writes, allow_unlocked_writes
FROM namespaces
ORDER BY name;

-- name: SetNamespaceOverrides :execrows
-- Replaces the override switches of one namespace (ADR 44, decision 6).
UPDATE namespaces
SET allow_forced_writes = $1, allow_unlocked_writes = $2
WHERE name = $3;

-- name: GetNamespaceForShare :one
-- Reads the namespace row with a shared lock. A switch change waits for the
-- transaction that reads it, so the switch it reads is the one that commits.
SELECT name, home_region, failover_version, created_at_ms, allow_forced_writes, allow_unlocked_writes
FROM namespaces
WHERE name = $1
FOR SHARE;

-- name: GetFailoverVersionForShare :one
-- Locks the namespace row so a concurrent promotion waits for this write.
SELECT failover_version
FROM namespaces
WHERE name = $1
FOR SHARE;

-- name: GetFailoverVersionForUpdate :one
-- Locks the namespace row FOR UPDATE so concurrent audit appends of one
-- namespace wait for each other: the next append reads the new last entry.
-- A promotion still waits the same way, so the fence still holds.
SELECT failover_version
FROM namespaces
WHERE name = $1
FOR UPDATE;

-- name: CreateStack :execrows
INSERT INTO stacks (namespace, project, name, id, version, created_at_ms, updated_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT DO NOTHING;

-- name: GetStack :one
SELECT namespace, project, name, id, version, created_at_ms, updated_at_ms
FROM stacks
WHERE namespace = $1 AND project = $2 AND name = $3;

-- name: ListStacksInNamespace :many
SELECT namespace, project, name, id, version, created_at_ms, updated_at_ms
FROM stacks
WHERE namespace = $1 AND deleted_at_ms IS NULL
ORDER BY project, name;

-- name: ListStacksInProject :many
SELECT namespace, project, name, id, version, created_at_ms, updated_at_ms
FROM stacks
WHERE namespace = $1 AND project = $2 AND deleted_at_ms IS NULL
ORDER BY name;

-- name: CreateStackTag :exec
INSERT INTO stack_tags (stack_id, name, value)
VALUES ($1, $2, $3);

-- name: GetStackTags :many
SELECT name, value
FROM stack_tags
WHERE stack_id = $1
ORDER BY name;

-- name: ListStackTagsInNamespace :many
SELECT t.stack_id, t.name, t.value
FROM stack_tags AS t
JOIN stacks AS s ON s.id = t.stack_id
WHERE s.namespace = $1 AND s.deleted_at_ms IS NULL
ORDER BY t.stack_id, t.name;

-- name: DeleteStackTags :exec
DELETE FROM stack_tags
WHERE stack_id = $1;

-- name: TouchStack :exec
UPDATE stacks
SET updated_at_ms = $1
WHERE id = $2;

-- name: RenameStack :execrows
UPDATE stacks SET name = $1 WHERE namespace = $2 AND project = $3 AND name = $4;

-- name: GetStackID :one
SELECT id
FROM stacks
WHERE namespace = $1 AND project = $2 AND name = $3;

-- name: LockStackForUpdates :one
-- Serializes starts and ends of the stack's updates. NO KEY UPDATE does not
-- block the foreign-key checks of checkpoint and event writes.
SELECT id, version
FROM stacks
WHERE namespace = $1 AND project = $2 AND name = $3
FOR NO KEY UPDATE;

-- name: CreateUpdate :exec
INSERT INTO updates (
    stack_id, id, kind, dry_run, status, version, revision,
    lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms,
    created_by, restored_from
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14);

-- name: GetUpdate :one
SELECT u.stack_id, u.id, u.kind, u.dry_run, u.status, u.version, u.revision,
       u.lease_digest, u.lease_expires_at_ms, u.created_at_ms, u.started_at_ms, u.ended_at_ms,
       u.created_by, u.restored_from, u.journal_version,
       COALESCE(c.size, octet_length(c.deployment), 0)::BIGINT AS checkpoint_bytes
FROM updates AS u
LEFT JOIN checkpoints AS c ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE u.stack_id = $1 AND u.id = $2;

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
WHERE stack_id = $1
  AND status = 'running' AND NOT dry_run AND kind <> 'preview';

-- name: HighestUpdateVersion :one
SELECT COALESCE(MAX(version), 0)::BIGINT
FROM updates
WHERE stack_id = $1
  AND status <> 'not started' AND NOT dry_run AND kind <> 'preview';

-- name: SetStackVersion :exec
UPDATE stacks
SET version = $1, updated_at_ms = $2
WHERE namespace = $3 AND project = $4 AND name = $5;

-- name: ActiveUpdate :one
SELECT u.stack_id, u.id, u.kind, u.dry_run, u.status, u.version, u.revision,
       u.lease_digest, u.lease_expires_at_ms, u.created_at_ms, u.started_at_ms, u.ended_at_ms,
       u.created_by, u.restored_from, u.journal_version,
       COALESCE(c.size, octet_length(c.deployment), 0)::BIGINT AS checkpoint_bytes
FROM updates AS u
LEFT JOIN checkpoints AS c ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE u.stack_id = $1
  AND u.status = 'running' AND NOT u.dry_run AND u.kind <> 'preview';

-- name: ListUpdates :many
SELECT u.stack_id, u.id, u.kind, u.dry_run, u.status, u.version, u.revision,
       u.lease_digest, u.lease_expires_at_ms, u.created_at_ms, u.started_at_ms, u.ended_at_ms,
       u.created_by, u.restored_from, u.journal_version,
       COALESCE(c.size, octet_length(c.deployment), 0)::BIGINT AS checkpoint_bytes
FROM updates AS u
LEFT JOIN checkpoints AS c ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE u.stack_id = $1
  AND u.status <> 'not started' AND NOT u.dry_run AND u.kind <> 'preview'
ORDER BY u.version DESC
LIMIT $2 OFFSET $3;

-- name: UpdateExists :one
SELECT EXISTS (
    SELECT 1 FROM updates WHERE stack_id = $1 AND id = $2
);

-- name: GetUpdateLeaseForShare :one
-- Locks the update so a concurrent cancel waits until this write commits.
SELECT status, lease_digest
FROM updates
WHERE stack_id = $1 AND id = $2
FOR SHARE;

-- name: GetUpdateKindForShare :one
-- Locks the update so a concurrent cancel waits until this write commits.
SELECT kind, dry_run
FROM updates
WHERE stack_id = $1 AND id = $2
FOR SHARE;

-- name: GetLatestCheckpoint :one
SELECT c.stack_id, c.update_id, c.lease_digest, c.schema_version, c.sequence,
       c.digest, c.invalid, c.features, c.deployment, c.saved_at_ms, c.resource_count,
       c.blob_key, c.size, c.blob_size, c.blob_md5
FROM checkpoints AS c
JOIN updates AS u ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE c.stack_id = $1
ORDER BY u.version DESC
LIMIT 1;

-- name: GetCheckpointByUpdate :one
SELECT stack_id, update_id, lease_digest, schema_version, sequence,
       digest, invalid, features, deployment, saved_at_ms, resource_count,
       blob_key, size, blob_size, blob_md5
FROM checkpoints
WHERE stack_id = $1 AND update_id = $2;

-- name: GetCheckpointByVersion :one
SELECT c.stack_id, c.update_id, c.lease_digest, c.schema_version, c.sequence,
       c.digest, c.invalid, c.features, c.deployment, c.saved_at_ms, c.resource_count,
       c.blob_key, c.size, c.blob_size, c.blob_md5
FROM checkpoints AS c
JOIN updates AS u ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE c.stack_id = $1 AND u.version = $2;

-- name: PutCheckpoint :exec
INSERT INTO checkpoints (
    stack_id, update_id, lease_digest, schema_version, sequence,
    digest, invalid, features, deployment, saved_at_ms, resource_count,
    blob_key, size, blob_size, blob_md5
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (stack_id, update_id) DO UPDATE SET
    lease_digest = excluded.lease_digest,
    schema_version = excluded.schema_version,
    sequence = excluded.sequence,
    digest = excluded.digest,
    invalid = excluded.invalid,
    features = excluded.features,
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
WHERE stack_id = $1 AND blob_key IS NOT NULL AND blob_key <> ''
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
    WHERE c.stack_id = $1
    ORDER BY u.version DESC
    LIMIT 1
);

-- name: LatestDeployment :one
-- The deployment of the stack's latest checkpoint, for the delete guard of a
-- checkpoint that predates the stored count. It loads one deployment only.
SELECT c.deployment
FROM checkpoints AS c
JOIN updates AS u ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE c.stack_id = $1
ORDER BY u.version DESC
LIMIT 1;

-- name: SoftDeleteStack :execrows
-- Moves the name to deleted_name and frees it (ADR 47). A stack name cannot
-- hold ":", so "deleted:" and the ID never match a lookup by name.
UPDATE stacks
SET deleted_name = name, name = 'deleted:' || id, deleted_at_ms = $1
WHERE id = $2 AND deleted_at_ms IS NULL;

-- name: DeleteStack :execrows
DELETE FROM stacks
WHERE namespace = $1 AND project = $2 AND name = $3;

-- name: AppendEvent :exec
INSERT INTO update_events (stack_id, update_id, sequence, timestamp_ms, payload)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT DO NOTHING;

-- name: ListEvents :many
SELECT sequence, timestamp_ms, payload
FROM update_events
WHERE stack_id = $1 AND update_id = $2 AND sequence > $3
ORDER BY sequence
LIMIT $4;

-- name: CreateSecretKey :execrows
INSERT INTO secret_keys (namespace, stack_id, wrapper_id, ciphertext, created_at_ms)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT DO NOTHING;

-- name: GetSecretKey :one
SELECT namespace, stack_id, wrapper_id, ciphertext, created_at_ms
FROM secret_keys
WHERE namespace = $1 AND stack_id = $2;

-- name: ListSecretWrapperIDs :many
SELECT DISTINCT wrapper_id
FROM secret_keys
ORDER BY wrapper_id;

-- name: DeleteSecretKeysOfStack :exec
DELETE FROM secret_keys
WHERE stack_id = $1;

-- name: UpsertInventory :exec
-- The lock columns stay out of the upsert: other writes keep the lock a
-- running update or a held workspace published, and only UpdateInventoryLock
-- changes it.
INSERT INTO inventory (namespace, tool, name, unit_id, display_name, last_changed_ms, status, resource_count)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
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
SET lock_holder = $1, lock_since_ms = $2, lock_operation = $3
WHERE namespace = $4 AND tool = $5 AND name = $6;

-- name: InsertDeletedInventory :exec
-- Records a deleted unit (ADR 47). The delete transaction removes the live
-- entry of the unit with DeleteInventory.
INSERT INTO inventory_deleted (namespace, tool, unit_id, name, deleted_at_ms, purge_after_ms)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListDeletedInventory :many
SELECT namespace, tool, unit_id, name, deleted_at_ms, purge_after_ms
FROM inventory_deleted
WHERE namespace = $1
ORDER BY tool, name, unit_id;

-- name: DeleteInventory :exec
DELETE FROM inventory
WHERE namespace = $1 AND tool = $2 AND name = $3;

-- name: ListInventory :many
SELECT namespace, tool, name, unit_id, display_name, last_changed_ms, status, resource_count,
       lock_holder, lock_since_ms, lock_operation
FROM inventory
WHERE namespace = $1
ORDER BY tool, name;

-- name: CreateWorkspace :execrows
INSERT INTO workspaces (
    id, namespace, name, revision, lock_id, lock_holder, lock_reason,
    locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms,
    allow_forced_writes, allow_unlocked_writes
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT DO NOTHING;

-- name: GetWorkspace :one
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes
FROM workspaces
WHERE namespace = $1 AND name = $2;

-- name: GetWorkspaceByID :one
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes
FROM workspaces
WHERE id = $1;

-- name: LockWorkspaceForFinalize :one
-- Locks the workspace row so two finalizes on one workspace cannot both pass
-- the serial check.
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes
FROM workspaces
WHERE id = $1
FOR UPDATE;

-- name: ListWorkspaces :many
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes
FROM workspaces
WHERE namespace = $1
ORDER BY name;

-- name: SaveWorkspace :execrows
-- A compare-and-swap: it changes nothing unless the stored revision is the
-- one the caller read. The current state pointer stays: only FinalizeStateVersion moves it.
UPDATE workspaces
SET revision = @revision, lock_id = @lock_id, lock_holder = @lock_holder,
    lock_reason = @lock_reason, locked_at_ms = @locked_at_ms, updated_at_ms = @updated_at_ms,
    allow_forced_writes = @allow_forced_writes, allow_unlocked_writes = @allow_unlocked_writes
WHERE id = @id AND revision = @expected_revision;

-- name: WorkspaceExists :one
SELECT EXISTS (
    SELECT 1 FROM workspaces WHERE id = $1
);

-- name: DeleteWorkspace :execrows
-- Removes one workspace. State versions go through DeleteStateVersionsOfWorkspace,
-- the data key through DeleteSecretKeysOfStack with the workspace ID.
DELETE FROM workspaces
WHERE namespace = $1 AND name = $2;

-- name: DeleteStateVersionsOfWorkspace :exec
-- Removes every state version of one workspace, pending and finalized.
DELETE FROM state_versions
WHERE workspace_id = $1;

-- name: CreateStateVersion :exec
INSERT INTO state_versions (
    id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
    force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
    restored_from, sealed, sensitive_marks, fenced_by, unlocked_write
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21
);

-- name: GetStateVersion :one
SELECT id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
       force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
       restored_from, sealed, sensitive_marks, fenced_by, unlocked_write
FROM state_versions
WHERE id = $1;

-- name: ListStateVersions :many
-- Finalized versions of one workspace, newest first. Ties on finalize time
-- break by ID, as the port comment promises. The caller normalizes the page.
SELECT id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
       force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
       restored_from, sealed, sensitive_marks, fenced_by, unlocked_write
FROM state_versions
WHERE workspace_id = $1 AND status = 'finalized'
ORDER BY finalized_at_ms DESC, id ASC
LIMIT $2 OFFSET $3;

-- name: CountStateVersions :one
SELECT COUNT(*)
FROM state_versions
WHERE workspace_id = $1 AND status = 'finalized';

-- name: CountPendingStateVersions :one
SELECT COUNT(*)
FROM state_versions
WHERE workspace_id = $1 AND status = 'pending';

-- name: LockWorkspaceForDeclare :one
-- Locks the workspace row so concurrent declares cannot both pass the
-- pending cap.
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes
FROM workspaces
WHERE id = $1
FOR UPDATE;

-- name: ListStateContentKeys :many
-- Every blob key the versions of one workspace name, without the empty ones.
SELECT content_key
FROM state_versions
WHERE workspace_id = $1 AND content_key <> '';

-- name: ListExpiredPendingStateVersions :many
-- Expired pending versions of one workspace, oldest expiry first. Ties on
-- expiry break by ID, as the port comment promises.
SELECT id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
       force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
       restored_from, sealed, sensitive_marks, fenced_by, unlocked_write
FROM state_versions
WHERE workspace_id = $1 AND status = 'pending' AND upload_expires_at_ms < $2
ORDER BY upload_expires_at_ms ASC, id ASC;

-- name: DeleteStateVersion :execrows
-- Deletes one pending version. A finalized version matches no row, so a
-- concurrent finalize wins over a sweep: the sweep then skips the version.
DELETE FROM state_versions
WHERE id = $1 AND status = 'pending';

-- name: SetPendingContentKey :execrows
-- Notes one upload attempt on a pending version. A finalized version
-- matches no row, so a late attempt reports no change.
UPDATE state_versions
SET content_key = $1
WHERE id = $2 AND status = 'pending';

-- name: ListUnsealedStateVersions :many
-- Finalized versions that still hold plain content, ordered by ID, so the
-- seal job seals them in a stable order. A version without content stays
-- out: a pending row names no content yet.
SELECT id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
       force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
       restored_from, sealed, sensitive_marks, fenced_by, unlocked_write
FROM state_versions
WHERE NOT sealed AND status = 'finalized' AND content_key <> ''
ORDER BY id ASC;

-- name: SealStateVersion :execrows
-- Moves one unsealed version to its sealed blob with its sealed outputs and
-- marks it sealed. The write matches only an unsealed row, so a retry after
-- a crash succeeds and changes nothing.
UPDATE state_versions
SET content_key = @content_key, outputs = @outputs, sealed = TRUE
WHERE id = @id AND NOT sealed;

-- name: SaveSensitiveMarks :execrows
-- Stores the secret paths of the json-state rendering of one version. It
-- works on a pending and on a finalized version: the rendering may land
-- after the state upload finalizes. A missing version matches no row.
UPDATE state_versions
SET sensitive_marks = $1
WHERE id = $2;

-- name: GetCurrentStateVersion :one
-- Follows the workspace pointer: a forced upload may carry a lower serial and
-- still be current, so the highest serial is not always the current state.
SELECT sv.id, sv.workspace_id, sv.status, sv.serial, sv.lineage, sv.md5, sv.size,
       sv.content_key, sv.outputs, sv.force, sv.upload_digest, sv.upload_expires_at_ms,
       sv.holder, sv.lock_id, sv.created_at_ms, sv.finalized_at_ms, sv.restored_from, sv.sealed,
       sv.sensitive_marks, sv.fenced_by, unlocked_write
FROM state_versions AS sv
JOIN workspaces AS w ON w.current_state_version_id = sv.id
WHERE w.id = $1;

-- name: FinalizeStateVersion :execrows
-- Marks one pending version finalized with its recorded attempt. The write
-- matches only the recorded content key, so a superseded attempt reports no
-- change instead of finalizing over a newer one; a finalized version also
-- matches no row, so a retry reports no change. It also sets the declared
-- serial, lineage, MD5 and outputs: a streamed write declares them only after
-- its content is read (ADR 49, decision 7), and its row holds placeholders
-- until then.
UPDATE state_versions
SET serial = @serial, lineage = @lineage, md5 = @md5, outputs = @outputs, status = 'finalized', finalized_at_ms = @finalized_at_ms, size = @size
WHERE id = @id AND status = 'pending' AND content_key = @content_key;

-- name: SetWorkspaceCurrent :exec
UPDATE workspaces
SET current_state_version_id = $1, updated_at_ms = $2
WHERE id = $3;

-- name: CreateBackup :execrows
INSERT INTO backups (id, state, started_at_ms, ended_at_ms, target, error)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT DO NOTHING;

-- name: GetBackup :one
SELECT id, state, started_at_ms, ended_at_ms, target, error
FROM backups
WHERE id = $1;

-- name: ListBackups :many
SELECT id, state, started_at_ms, ended_at_ms, target, error
FROM backups
ORDER BY started_at_ms DESC, id ASC;

-- name: FinishBackup :execrows
UPDATE backups
SET state = $1, error = $2, ended_at_ms = $3
WHERE id = $4 AND state = 'running';

-- name: FailRunningBackups :exec
UPDATE backups
SET state = 'failed', error = $1, ended_at_ms = $2
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
VALUES (1, $1)
ON CONFLICT (id) DO UPDATE SET state = excluded.state;

-- name: SetInstallationStateIf :execrows
-- Stores next only when the stored state is expected: a compare-and-swap, so
-- enters on several instances of one database stay ordered. It changes
-- nothing when the stored state differs or the row is missing.
UPDATE installation
SET state = $1
WHERE id = 1 AND state = $2;

-- name: CreateRestore :execrows
INSERT INTO restores (id, state, backup_id, pre_backup_id, requested_by, started_at_ms, ended_at_ms, error)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT DO NOTHING;

-- name: GetRestore :one
SELECT id, state, backup_id, pre_backup_id, requested_by, started_at_ms, ended_at_ms, error
FROM restores
WHERE id = $1;

-- name: ListRestores :many
SELECT id, state, backup_id, pre_backup_id, requested_by, started_at_ms, ended_at_ms, error
FROM restores
ORDER BY started_at_ms DESC, id ASC;

-- name: FinishRestore :execrows
UPDATE restores
SET state = $1, error = $2, ended_at_ms = $3
WHERE id = $4 AND state IN ('checking', 'applying');

-- name: MarkRestoreApplying :execrows
-- Moves one checking restore to applying with the backup taken before the
-- apply. Any other state matches no row, so a retry reports no change.
UPDATE restores
SET state = 'applying', pre_backup_id = $1
WHERE id = $2 AND state = 'checking';

-- name: FailRunningRestores :exec
UPDATE restores
SET state = 'failed', error = $1, ended_at_ms = $2
WHERE state IN ('checking', 'applying');

-- name: BumpFailoverVersions :execrows
-- Increments the failover version of every namespace by one (ADR 38
-- decision 7), so leases and locks from before the restore stop working.
UPDATE namespaces
SET failover_version = failover_version + 1;

-- name: BackupHomeRegions :many
SELECT DISTINCT home_region
FROM namespaces
ORDER BY home_region;

-- name: BackupWrapperIDs :many
SELECT DISTINCT wrapper_id
FROM secret_keys
ORDER BY wrapper_id;

-- name: BackupBlobs :many
-- Every blob key the state versions and the checkpoints name, with the size
-- and MD5 of the plain bytes and the sealed flag, ordered by key. Sealed
-- state blobs are stored sealed: the backup opens them to check and stores
-- the sealed bytes.
SELECT content_key, size, md5, sealed
FROM state_versions
WHERE content_key <> ''
UNION ALL
SELECT blob_key AS content_key, blob_size AS size, blob_md5 AS md5, FALSE AS sealed
FROM checkpoints
WHERE blob_key IS NOT NULL AND blob_key <> ''
UNION ALL
SELECT blob_key AS content_key, size AS size, md5 AS md5, FALSE AS sealed
FROM update_journal
ORDER BY content_key;

-- name: LastAuditEntry :one
-- The entry with the highest sequence of a namespace, for the next append.
-- A namespace with no entries matches no row.
SELECT namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
       at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count
FROM audit_entries
WHERE namespace = $1
ORDER BY sequence DESC
LIMIT 1;

-- name: InsertAuditEntry :exec
INSERT INTO audit_entries (
    namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
    at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19);

-- name: ListAuditEntries :many
-- One page of the entries of a namespace, newest first. Ties on time break
-- by sequence, as the port comment promises.
SELECT namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
       at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count
FROM audit_entries
WHERE namespace = $1
ORDER BY sequence DESC
LIMIT $2 OFFSET $3;

-- name: CountAuditEntries :one
SELECT COUNT(*)
FROM audit_entries
WHERE namespace = $1;

-- name: ListAuditChainPage :many
-- One bounded page of the chain of a namespace oldest first, for the chain
-- check. The check reads the chain with this query in pages of
-- audit.CheckPageSize entries after the last sequence it saw, so a namespace
-- with a large log never grows the check without a bound.
SELECT namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
       at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count
FROM audit_entries
WHERE namespace = $1 AND sequence > $2
ORDER BY sequence ASC
LIMIT $3;

-- name: JournalSequenceSHA :one
SELECT sha256 FROM update_journal_sequences
WHERE stack_id = $1 AND update_id = $2 AND sequence = $3;

-- name: NextJournalArrival :one
SELECT (COALESCE(MAX(arrival), 0) + 1)::BIGINT AS arrival
FROM update_journal
WHERE stack_id = $1 AND update_id = $2;

-- name: InsertJournal :exec
INSERT INTO update_journal (
    stack_id, update_id, arrival, blob_key, sha256, md5, size,
    lowest_sequence, sequence_ids, operation_ids
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: InsertJournalSequence :exec
INSERT INTO update_journal_sequences (stack_id, update_id, sequence, arrival, sha256)
VALUES ($1, $2, $3, $4, $5);

-- name: LockUpdateForJournal :one
-- Locks the update FOR UPDATE so two batches of one update cannot read the same
-- last arrival or both store the same batch: the second waits for the first.
SELECT id
FROM updates
WHERE stack_id = $1 AND id = $2
FOR UPDATE;

-- name: ListJournalOperations :many
SELECT operation_ids FROM update_journal
WHERE stack_id = $1 AND update_id = $2
ORDER BY arrival;

-- name: JournalBlobKeys :many
SELECT blob_key FROM update_journal
WHERE stack_id = $1 AND update_id = $2
ORDER BY arrival;

-- name: DeleteJournalSequences :exec
DELETE FROM update_journal_sequences
WHERE stack_id = $1 AND update_id = $2;

-- name: DeleteJournal :exec
DELETE FROM update_journal
WHERE stack_id = $1 AND update_id = $2;

-- name: JournalState :one
SELECT journal_closed, status FROM updates
WHERE stack_id = $1 AND id = $2;

-- name: CloseJournal :exec
UPDATE updates SET journal_closed = TRUE
WHERE stack_id = $1 AND id = $2;
