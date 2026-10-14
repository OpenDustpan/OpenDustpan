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
WHERE namespace = $1 AND project = $2 AND name = $3 AND deleted_at_ms IS NULL;

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
UPDATE stacks SET name = $1 WHERE namespace = $2 AND project = $3 AND name = $4 AND deleted_at_ms IS NULL;

-- name: GetStackID :one
SELECT id
FROM stacks
WHERE namespace = $1 AND project = $2 AND name = $3 AND deleted_at_ms IS NULL;

-- name: LockStackForUpdates :one
-- Serializes starts and ends of the stack's updates. NO KEY UPDATE does not
-- block the foreign-key checks of checkpoint and event writes.
SELECT id, version
FROM stacks
WHERE namespace = $1 AND project = $2 AND name = $3 AND deleted_at_ms IS NULL
FOR NO KEY UPDATE;

-- name: CreateUpdate :exec
INSERT INTO updates (
    stack_id, id, kind, dry_run, status, version, revision,
    lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms,
    created_by, restored_from, token_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15);

-- name: GetUpdate :one
SELECT u.stack_id, u.id, u.kind, u.dry_run, u.status, u.version, u.revision,
       u.lease_digest, u.lease_expires_at_ms, u.created_at_ms, u.started_at_ms, u.ended_at_ms,
       u.created_by, u.restored_from, u.journal_version, u.token_id,
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
    journal_version = @journal_version, token_id = @token_id
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
WHERE namespace = $3 AND project = $4 AND name = $5 AND deleted_at_ms IS NULL;

-- name: ActiveUpdate :one
SELECT u.stack_id, u.id, u.kind, u.dry_run, u.status, u.version, u.revision,
       u.lease_digest, u.lease_expires_at_ms, u.created_at_ms, u.started_at_ms, u.ended_at_ms,
       u.created_by, u.restored_from, u.journal_version, u.token_id,
       COALESCE(c.size, octet_length(c.deployment), 0)::BIGINT AS checkpoint_bytes
FROM updates AS u
LEFT JOIN checkpoints AS c ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE u.stack_id = $1
  AND u.status = 'running' AND NOT u.dry_run AND u.kind <> 'preview';

-- name: ListUpdates :many
SELECT u.stack_id, u.id, u.kind, u.dry_run, u.status, u.version, u.revision,
       u.lease_digest, u.lease_expires_at_ms, u.created_at_ms, u.started_at_ms, u.ended_at_ms,
       u.created_by, u.restored_from, u.journal_version, u.token_id,
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
-- Byte order (COLLATE "C"), as QueryDeletedUnits and inventory.PageDeleted
-- order, whatever the collation of the database.
SELECT namespace, tool, unit_id, name, deleted_at_ms, purge_after_ms
FROM inventory_deleted
WHERE namespace = $1
ORDER BY tool COLLATE "C", name COLLATE "C", unit_id COLLATE "C";

-- name: ListExpiredInventory :many
-- The first page of the deleted units of every namespace whose retention
-- ended at or before $1 (ADR 47), for the retention sweep. Byte order
-- (COLLATE "C"), as inventory.PageExpired orders.
SELECT namespace, tool, unit_id, name, deleted_at_ms, purge_after_ms
FROM inventory_deleted
WHERE purge_after_ms <= $1
ORDER BY purge_after_ms, tool COLLATE "C", unit_id COLLATE "C"
LIMIT $2;

-- name: ListExpiredInventoryAfter :many
-- The page of ListExpiredInventory after the cursor (end of retention,
-- tool, unit ID).
SELECT namespace, tool, unit_id, name, deleted_at_ms, purge_after_ms
FROM inventory_deleted
WHERE purge_after_ms <= sqlc.arg(now_ms)
  AND (purge_after_ms > sqlc.arg(after_ms)
    OR (purge_after_ms = sqlc.arg(after_ms) AND tool COLLATE "C" > sqlc.arg(after_tool))
    OR (purge_after_ms = sqlc.arg(after_ms) AND tool = sqlc.arg(after_tool)
      AND unit_id COLLATE "C" > sqlc.arg(after_unit_id)))
ORDER BY purge_after_ms, tool COLLATE "C", unit_id COLLATE "C"
LIMIT sqlc.arg(page_limit);

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
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes,
       deleted_at_ms, deleted_name
FROM workspaces
WHERE namespace = $1 AND name = $2 AND deleted_at_ms IS NULL;

-- name: GetWorkspaceByID :one
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes,
       deleted_at_ms, deleted_name
FROM workspaces
WHERE id = $1 AND deleted_at_ms IS NULL;

-- name: LockWorkspaceForFinalize :one
-- Locks the workspace row so two finalizes on one workspace cannot both pass
-- the serial check.
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes,
       deleted_at_ms, deleted_name
FROM workspaces
WHERE id = $1 AND deleted_at_ms IS NULL
FOR UPDATE;

-- name: ListWorkspaces :many
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes,
       deleted_at_ms, deleted_name
FROM workspaces
WHERE namespace = $1 AND deleted_at_ms IS NULL
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
    SELECT 1 FROM workspaces WHERE id = $1 AND deleted_at_ms IS NULL
);

-- name: SoftDeleteWorkspace :execrows
-- Moves the name to deleted_name and frees it (ADR 47). A workspace name
-- cannot hold ":", so "deleted:" and the ID never match a lookup by name. The
-- revision moves on, so a concurrent compare-and-swap of the row fails.
UPDATE workspaces
SET deleted_name = name, name = 'deleted:' || id, deleted_at_ms = $1, revision = revision + 1
WHERE id = $2 AND deleted_at_ms IS NULL;

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
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes,
       deleted_at_ms, deleted_name
FROM workspaces
WHERE id = $1 AND deleted_at_ms IS NULL
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
       at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count, changes, object_id
FROM audit_entries
WHERE namespace = $1
ORDER BY sequence DESC
LIMIT 1;

-- name: InsertAuditEntry :exec
INSERT INTO audit_entries (
    namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
    at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count, changes, object_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21);

-- name: ListAuditEntries :many
-- One page of the entries of a namespace, newest first. Ties on time break
-- by sequence, as the port comment promises.
SELECT namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
       at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count, changes, object_id
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
       at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count, changes, object_id
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

-- name: CreatePrincipal :execrows
INSERT INTO principals (id, subject, display_name, kind, source, created_at_ms, updated_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT DO NOTHING;

-- name: GetPrincipal :one
SELECT p.id, p.subject, p.display_name, p.kind, p.source, p.created_at_ms, p.updated_at_ms, p.disabled_at_ms,
  (SELECT COUNT(*) FROM group_members m WHERE m.principal_id = p.id AND m.removed_at_ms IS NULL) AS group_count
FROM principals p WHERE p.id = $1 AND p.deleted_at_ms IS NULL;

-- name: GetPrincipalBySubject :one
SELECT p.id, p.subject, p.display_name, p.kind, p.source, p.created_at_ms, p.updated_at_ms, p.disabled_at_ms,
  (SELECT COUNT(*) FROM group_members m WHERE m.principal_id = p.id AND m.removed_at_ms IS NULL) AS group_count
FROM principals p WHERE p.subject COLLATE "C" = @subject::text AND p.deleted_at_ms IS NULL;

-- name: ListPrincipals :many
SELECT p.id, p.subject, p.display_name, p.kind, p.source, p.created_at_ms, p.updated_at_ms, p.disabled_at_ms,
  (SELECT COUNT(*) FROM group_members m WHERE m.principal_id = p.id AND m.removed_at_ms IS NULL) AS group_count
FROM principals p
WHERE p.deleted_at_ms IS NULL
  AND p.subject COLLATE "C" > @after::text
  AND p.subject COLLATE "C" >= @low::text
  AND (@high::text = '' OR p.subject COLLATE "C" < @high::text)
  AND (@kind::text = '' OR p.kind = @kind::text)
ORDER BY p.subject COLLATE "C"
LIMIT @row_limit;

-- name: CountPrincipals :one
SELECT COUNT(*) FROM principals
WHERE deleted_at_ms IS NULL
  AND subject COLLATE "C" >= @low::text
  AND (@high::text = '' OR subject COLLATE "C" < @high::text)
  AND (@kind::text = '' OR kind = @kind::text);

-- name: UpdatePrincipal :execrows
UPDATE principals SET display_name = $1, updated_at_ms = $2
WHERE id = $3 AND deleted_at_ms IS NULL;

-- name: DeletePrincipal :execrows
UPDATE principals SET deleted_at_ms = $1
WHERE id = $2 AND deleted_at_ms IS NULL;

-- name: EndMembershipsOfPrincipal :exec
UPDATE group_members SET removed_at_ms = $1
WHERE principal_id = $2 AND removed_at_ms IS NULL;

-- name: CreateGroup :execrows
INSERT INTO principal_groups (id, name, name_key, description, source, created_at_ms, updated_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT DO NOTHING;

-- name: GetGroup :one
SELECT g.id, g.name, g.description, g.source, g.created_at_ms, g.updated_at_ms,
  (SELECT COUNT(*) FROM group_members m WHERE m.group_id = g.id AND m.removed_at_ms IS NULL) AS member_count
FROM principal_groups g WHERE g.id = $1 AND g.deleted_at_ms IS NULL;

-- name: GroupIDByNameKey :one
SELECT id FROM principal_groups WHERE name_key COLLATE "C" = @name_key::text AND deleted_at_ms IS NULL;

-- name: ListGroups :many
SELECT g.id, g.name, g.description, g.source, g.created_at_ms, g.updated_at_ms,
  (SELECT COUNT(*) FROM group_members m WHERE m.group_id = g.id AND m.removed_at_ms IS NULL) AS member_count
FROM principal_groups g
WHERE g.deleted_at_ms IS NULL
  AND g.name_key COLLATE "C" > @after::text
  AND g.name_key COLLATE "C" >= @low::text
  AND (@high::text = '' OR g.name_key COLLATE "C" < @high::text)
ORDER BY g.name_key COLLATE "C"
LIMIT @row_limit;

-- name: CountGroups :one
SELECT COUNT(*) FROM principal_groups
WHERE deleted_at_ms IS NULL
  AND name_key COLLATE "C" >= @low::text
  AND (@high::text = '' OR name_key COLLATE "C" < @high::text);

-- name: UpdateGroup :execrows
UPDATE principal_groups SET name = $1, name_key = $2, description = $3, updated_at_ms = $4
WHERE id = $5 AND deleted_at_ms IS NULL;

-- name: DeleteGroup :execrows
UPDATE principal_groups SET deleted_at_ms = $1
WHERE id = $2 AND deleted_at_ms IS NULL;

-- name: EndMembershipsInGroup :exec
UPDATE group_members SET removed_at_ms = $1
WHERE group_id = $2 AND removed_at_ms IS NULL;

-- name: PrincipalExists :one
SELECT COUNT(*) FROM principals WHERE id = $1 AND deleted_at_ms IS NULL;

-- name: GroupSource :one
SELECT source FROM principal_groups WHERE id = $1 AND deleted_at_ms IS NULL;

-- name: AddGroupMember :execrows
INSERT INTO group_members (group_id, principal_id, added_at_ms)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: RemoveGroupMember :execrows
UPDATE group_members SET removed_at_ms = $1
WHERE group_id = $2 AND principal_id = $3 AND removed_at_ms IS NULL;

-- name: ListGroupMembers :many
SELECT p.id, p.subject, p.display_name, p.kind, p.source, p.created_at_ms, p.updated_at_ms, p.disabled_at_ms,
  (SELECT COUNT(*) FROM group_members o WHERE o.principal_id = p.id AND o.removed_at_ms IS NULL) AS group_count,
  m.added_at_ms
FROM group_members m JOIN principals p ON p.id = m.principal_id
WHERE m.group_id = @group_id AND m.removed_at_ms IS NULL
  AND p.subject COLLATE "C" > @after::text
  AND p.subject COLLATE "C" >= @low::text
  AND (@high::text = '' OR p.subject COLLATE "C" < @high::text)
ORDER BY p.subject COLLATE "C"
LIMIT @row_limit;

-- name: CountGroupMembers :one
SELECT COUNT(*)
FROM group_members m JOIN principals p ON p.id = m.principal_id
WHERE m.group_id = @group_id AND m.removed_at_ms IS NULL
  AND p.subject COLLATE "C" >= @low::text
  AND (@high::text = '' OR p.subject COLLATE "C" < @high::text);

-- name: ListGroupsOfPrincipal :many
SELECT g.id, g.name, g.description, g.source, g.created_at_ms, g.updated_at_ms,
  (SELECT COUNT(*) FROM group_members o WHERE o.group_id = g.id AND o.removed_at_ms IS NULL) AS member_count
FROM group_members m JOIN principal_groups g ON g.id = m.group_id
WHERE m.principal_id = @principal_id AND m.removed_at_ms IS NULL
  AND g.name_key COLLATE "C" > @after::text
  AND g.name_key COLLATE "C" >= @low::text
  AND (@high::text = '' OR g.name_key COLLATE "C" < @high::text)
ORDER BY g.name_key COLLATE "C"
LIMIT @row_limit;

-- name: CountGroupsOfPrincipal :one
SELECT COUNT(*)
FROM group_members m JOIN principal_groups g ON g.id = m.group_id
WHERE m.principal_id = @principal_id AND m.removed_at_ms IS NULL
  AND g.name_key COLLATE "C" >= @low::text
  AND (@high::text = '' OR g.name_key COLLATE "C" < @high::text);

-- name: LockServerAuditScope :exec
-- Orders the appends of the audit scope @server, which has no namespace row
-- to lock. The key is fixed: 0x6175646974 is "audit" in ASCII.
SELECT pg_advisory_xact_lock(418581342580);

-- name: CreateRole :execrows
INSERT INTO roles (id, name, name_key, description, actions, created_at_ms, updated_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT DO NOTHING;

-- name: GetRole :one
SELECT id, name, description, actions, created_at_ms, updated_at_ms FROM roles
WHERE id = $1 AND deleted_at_ms IS NULL;

-- name: RolesByIDs :many
SELECT id, name, description, actions, created_at_ms, updated_at_ms FROM roles
WHERE id = ANY(@ids::text[]) AND deleted_at_ms IS NULL;

-- name: RoleIDByNameKey :one
SELECT id FROM roles WHERE name_key COLLATE "C" = @name_key::text AND deleted_at_ms IS NULL;

-- name: ListRoles :many
SELECT id, name, description, actions, created_at_ms, updated_at_ms FROM roles
WHERE deleted_at_ms IS NULL
ORDER BY name_key COLLATE "C";

-- name: UpdateRole :execrows
UPDATE roles SET name = $1, name_key = $2, description = $3, actions = $4, updated_at_ms = $5
WHERE id = $6 AND deleted_at_ms IS NULL;

-- name: DeleteRole :execrows
UPDATE roles SET deleted_at_ms = $1 WHERE id = $2 AND deleted_at_ms IS NULL;

-- name: CountGrantsWithRole :one
SELECT COUNT(*) FROM grants WHERE role = $1 AND revoked_at_ms IS NULL;

-- name: CreateGrant :execrows
INSERT INTO grants (id, subject, subject_kind, subject_id, role, object, target, object_kind, namespace, condition, created_at_ms, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT DO NOTHING;

-- name: GetGrant :one
SELECT id, subject, subject_kind, subject_id, role, object, target, object_kind, namespace, condition, created_at_ms, created_by
FROM grants WHERE id = $1 AND revoked_at_ms IS NULL;

-- name: ListGrantsOn :many
SELECT id, subject, subject_kind, subject_id, role, object, target, object_kind, namespace, condition, created_at_ms, created_by
FROM grants
WHERE target = @target AND revoked_at_ms IS NULL
  AND (subject > @after_subject OR (subject = @after_subject AND role > @after_role))
ORDER BY subject, role
LIMIT @row_limit;

-- name: CountGrantsOn :one
SELECT COUNT(*) FROM grants WHERE target = @target AND revoked_at_ms IS NULL;

-- name: RevokeGrant :execrows
UPDATE grants SET revoked_at_ms = $1, revoked_by = $2 WHERE id = $3 AND revoked_at_ms IS NULL;

-- name: GrantsOn :many
SELECT id, subject, subject_kind, subject_id, role, object, target, object_kind, namespace, condition, created_at_ms, created_by
FROM grants
WHERE subject = ANY(@subjects::text[]) AND target = ANY(@targets::text[]) AND revoked_at_ms IS NULL
ORDER BY target, subject, role;

-- name: GrantsWithRoles :many
SELECT id, subject, subject_kind, subject_id, role, object, target, object_kind, namespace, condition, created_at_ms, created_by
FROM grants
WHERE subject = ANY(@subjects::text[]) AND role = ANY(@roles::text[]) AND revoked_at_ms IS NULL
ORDER BY target, subject, role;

-- name: RevokeGrantsOfSubject :exec
UPDATE grants SET revoked_at_ms = $1, revoked_by = $2
WHERE subject_kind = $3 AND subject_id = $4 AND revoked_at_ms IS NULL;

-- name: GrantsOfSubject :many
SELECT id, subject, subject_kind, subject_id, role, object, target, object_kind, namespace, condition, created_at_ms, created_by
FROM grants
WHERE subject_kind = $1 AND subject_id = $2 AND revoked_at_ms IS NULL
ORDER BY target, role;

-- name: CountRoleGrantsBelowInstallation :one
SELECT COUNT(*) FROM grants WHERE role = $1 AND object_kind <> 'installation' AND revoked_at_ms IS NULL;

-- name: CountEndedGrantRecords :one
-- The records of a new grant that have ended: its principal or group, and
-- its custom role. The grant write checks it in its own transaction, so a
-- delete that commits first wins.
SELECT (SELECT COUNT(*) FROM principals p WHERE @subject_kind::text = 'principal' AND p.id = @subject_id::text
          AND p.deleted_at_ms IS NOT NULL)
     + (SELECT COUNT(*) FROM principal_groups g WHERE @subject_kind::text = 'group' AND g.id = @subject_id::text
          AND g.deleted_at_ms IS NOT NULL)
     + (SELECT COUNT(*) FROM roles r WHERE r.id = @role::text AND r.deleted_at_ms IS NOT NULL);

-- name: SubjectsOfCaller :many
SELECT p.id AS principal_id, COALESCE(m.group_id, '')::text AS group_id
FROM principals p
LEFT JOIN group_members m ON m.principal_id = p.id AND m.removed_at_ms IS NULL
WHERE p.subject COLLATE "C" = @subject::text AND p.deleted_at_ms IS NULL;

-- name: ReviewGrants :many
-- One page of an access-review export (ADR 52, decision 10). The join on
-- group_members reads the memberships of the groups of the grants: of one
-- principal for a subject filter, or of every member for expand. It gives
-- one row for each membership that overlaps the grant and the window (a
-- history), or that is current at as_of_ms (a snapshot). In a history, a
-- grant on a container with a custom role is in the export, a deleted role
-- included. The IDs order with the C collation, as in SQLite.
SELECT g.id, g.subject, g.subject_kind, g.subject_id, g.role, g.object, g.target, g.condition,
  g.created_at_ms, g.created_by, g.revoked_at_ms, g.revoked_by,
  COALESCE(m.added_at_ms, 0)::bigint AS member_added_at_ms,
  m.removed_at_ms AS member_removed_at_ms,
  COALESCE(m.principal_id, '')::text AS member_id,
  COALESCE(mp.subject, '')::text AS member_subject,
  COALESCE(p.subject, pg.name, '')::text AS subject_name,
  (SELECT COUNT(*) FROM group_members c
   WHERE g.subject_kind = 'group' AND c.group_id = g.subject_id
     AND c.added_at_ms <= @as_of_ms::bigint
     AND COALESCE(c.removed_at_ms, 9223372036854775807) > @as_of_ms::bigint) AS member_count
FROM grants g
LEFT JOIN principals p ON g.subject_kind = 'principal' AND p.id = g.subject_id
LEFT JOIN principal_groups pg ON g.subject_kind = 'group' AND pg.id = g.subject_id
LEFT JOIN group_members m ON g.subject_kind = 'group' AND m.group_id = g.subject_id
  AND ((@principal::text <> '' AND m.principal_id = @principal::text)
    OR (@principal::text = '' AND @expand::boolean))
  AND CASE WHEN @history::boolean THEN
    m.added_at_ms < @to_ms::bigint
    AND COALESCE(m.removed_at_ms, 9223372036854775807) > @from_ms::bigint
    AND m.added_at_ms < COALESCE(g.revoked_at_ms, 9223372036854775807)
    AND COALESCE(m.removed_at_ms, 9223372036854775807) > g.created_at_ms
  ELSE m.added_at_ms <= @as_of_ms::bigint AND COALESCE(m.removed_at_ms, 9223372036854775807) > @as_of_ms::bigint END
LEFT JOIN principals mp ON mp.id = m.principal_id
WHERE (@all_grants::boolean
    OR (@namespace::text <> '' AND g.namespace = @namespace::text AND g.object_kind <> 'installation')
    OR g.target = @target::text
    OR (g.target = ANY(@containers::text[])
      AND (g.role = ANY(@container_roles::text[])
        OR (@history::boolean AND EXISTS (SELECT 1 FROM roles r WHERE r.id = g.role)))))
  AND (@subject::text = '' OR g.subject = @subject::text
    OR (@principal::text <> '' AND m.principal_id IS NOT NULL))
  AND CASE WHEN @history::boolean THEN
    g.created_at_ms < @to_ms::bigint AND COALESCE(g.revoked_at_ms, 9223372036854775807) > @from_ms::bigint
  ELSE g.created_at_ms <= @as_of_ms::bigint AND COALESCE(g.revoked_at_ms, 9223372036854775807) > @as_of_ms::bigint END
  AND (g.created_at_ms > @after_at_ms::bigint
    OR (g.created_at_ms = @after_at_ms::bigint
      AND (g.id COLLATE "C" > @after_id::text
        OR (g.id = @after_id::text
          AND (COALESCE(m.added_at_ms, 0) > @after_member_at_ms::bigint
            OR (COALESCE(m.added_at_ms, 0) = @after_member_at_ms::bigint
              AND COALESCE(m.principal_id, '') COLLATE "C" > @after_member_id::text))))))
ORDER BY g.created_at_ms, g.id COLLATE "C", member_added_at_ms, COALESCE(m.principal_id, '') COLLATE "C"
LIMIT @row_limit::bigint;

-- name: CountReviewGrants :one
SELECT COUNT(*)
FROM grants g
LEFT JOIN group_members m ON g.subject_kind = 'group' AND m.group_id = g.subject_id
  AND ((@principal::text <> '' AND m.principal_id = @principal::text)
    OR (@principal::text = '' AND @expand::boolean))
  AND CASE WHEN @history::boolean THEN
    m.added_at_ms < @to_ms::bigint
    AND COALESCE(m.removed_at_ms, 9223372036854775807) > @from_ms::bigint
    AND m.added_at_ms < COALESCE(g.revoked_at_ms, 9223372036854775807)
    AND COALESCE(m.removed_at_ms, 9223372036854775807) > g.created_at_ms
  ELSE m.added_at_ms <= @as_of_ms::bigint AND COALESCE(m.removed_at_ms, 9223372036854775807) > @as_of_ms::bigint END
WHERE (@all_grants::boolean
    OR (@namespace::text <> '' AND g.namespace = @namespace::text AND g.object_kind <> 'installation')
    OR g.target = @target::text
    OR (g.target = ANY(@containers::text[])
      AND (g.role = ANY(@container_roles::text[])
        OR (@history::boolean AND EXISTS (SELECT 1 FROM roles r WHERE r.id = g.role)))))
  AND (@subject::text = '' OR g.subject = @subject::text
    OR (@principal::text <> '' AND m.principal_id IS NOT NULL))
  AND CASE WHEN @history::boolean THEN
    g.created_at_ms < @to_ms::bigint AND COALESCE(g.revoked_at_ms, 9223372036854775807) > @from_ms::bigint
  ELSE g.created_at_ms <= @as_of_ms::bigint AND COALESCE(g.revoked_at_ms, 9223372036854775807) > @as_of_ms::bigint END;

-- name: ReviewStacksByIDs :many
-- The stacks of the unit grants of an export, deleted ones included (ADR 47):
-- an export shows a grant on a deleted unit, with its name at the time of
-- the delete, so a reviewer can clean it up.
SELECT id, namespace, project, CAST(COALESCE(deleted_name, name) AS TEXT) AS name, deleted_at_ms FROM stacks WHERE id = ANY(@ids::text[]);

-- name: ReviewWorkspacesByIDs :many
-- The workspaces of the unit grants of an export, deleted ones included.
SELECT id, namespace, CAST(COALESCE(deleted_name, name) AS TEXT) AS name, deleted_at_ms FROM workspaces WHERE id = ANY(@ids::text[]);

-- name: UnitStackID :one
-- The stable ID of a Pulumi stack, for a grant on the unit (ADR 19, ADR 52).
SELECT id FROM stacks WHERE namespace = $1 AND project = $2 AND name = $3 AND deleted_at_ms IS NULL;

-- name: UnitWorkspaceID :one
-- The stable ID of an OpenTofu workspace, for a grant on the unit.
SELECT id FROM workspaces WHERE namespace = $1 AND name = $2 AND deleted_at_ms IS NULL;

-- name: UnitStacksByIDs :many
-- A deleted stack (ADR 47) is left out: its grants give nothing until a
-- restore brings it back.
SELECT id, namespace, project, name FROM stacks WHERE id = ANY(@ids::text[]) AND deleted_at_ms IS NULL;

-- name: UnitWorkspacesByIDs :many
-- A deleted workspace (ADR 47) is left out: its grants give nothing until
-- a restore brings it back.
SELECT id, namespace, name FROM workspaces WHERE id = ANY(@ids::text[]) AND deleted_at_ms IS NULL;

-- Access tokens (ADR 53).

-- name: CreateAccessToken :execrows
INSERT INTO access_tokens (id, principal_id, name, kind, digest, created_at_ms, expires_at_ms, created_by)
SELECT sqlc.arg('id'), p.id, sqlc.arg('name'), sqlc.arg('kind'), sqlc.arg('digest'),
  sqlc.arg('created_at_ms'), sqlc.arg('expires_at_ms'), sqlc.arg('created_by')
FROM principals p
WHERE p.id = sqlc.arg('principal_id') AND p.deleted_at_ms IS NULL
ON CONFLICT DO NOTHING;

-- name: CountLivePrincipal :one
SELECT COUNT(*) FROM principals WHERE id = $1 AND deleted_at_ms IS NULL;

-- name: CountAccessToken :one
SELECT COUNT(*) FROM access_tokens WHERE id = $1;

-- name: GetAccessToken :one
SELECT id, principal_id, name, kind, created_at_ms, expires_at_ms, last_used_at_ms, revoked_at_ms, replaced_by, created_by, disabled_at_ms
FROM access_tokens WHERE id = $1;

-- name: GetAccessTokenByDigest :one
SELECT t.id, t.principal_id, t.name, t.kind, t.digest, t.created_at_ms, t.expires_at_ms, t.last_used_at_ms,
  t.revoked_at_ms, t.replaced_by, t.created_by, t.disabled_at_ms, p.subject, p.display_name, p.kind AS principal_kind,
  p.disabled_at_ms AS principal_disabled_at_ms,
  GREATEST(p.created_at_ms, p.last_active_at_ms, COALESCE((
    SELECT MAX(GREATEST(o.created_at_ms, COALESCE(o.last_used_at_ms, 0))) FROM access_tokens o WHERE o.principal_id = p.id
  ), 0))::bigint AS principal_active_at_ms
FROM access_tokens t
JOIN principals p ON p.id = t.principal_id
WHERE t.digest = $1 AND p.deleted_at_ms IS NULL;

-- name: ListAccessTokens :many
SELECT id, principal_id, name, kind, created_at_ms, expires_at_ms, last_used_at_ms, revoked_at_ms, replaced_by, created_by, disabled_at_ms
FROM access_tokens
WHERE (sqlc.arg('principal_id')::text = '' OR principal_id = sqlc.arg('principal_id')::text)
  AND (sqlc.arg('status')::bigint = 0
    OR ((sqlc.arg('status')::bigint = 1 OR sqlc.arg('status')::bigint = 2) AND revoked_at_ms IS NULL AND disabled_at_ms IS NULL AND expires_at_ms > sqlc.arg('at_ms')::bigint
      AND (sqlc.arg('status')::bigint = 1 OR expires_at_ms <= sqlc.arg('soon_ms')::bigint))
    OR (sqlc.arg('status')::bigint = 3 AND (revoked_at_ms IS NOT NULL OR disabled_at_ms IS NOT NULL OR expires_at_ms <= sqlc.arg('at_ms')::bigint)))
  AND (NOT sqlc.arg('has_after')::boolean
    OR created_at_ms < sqlc.arg('after_ms')::bigint
    OR (created_at_ms = sqlc.arg('after_ms')::bigint AND id COLLATE "C" < sqlc.arg('after_id')::text))
ORDER BY created_at_ms DESC, id COLLATE "C" DESC
LIMIT sqlc.arg('row_limit');

-- name: CountAccessTokens :one
SELECT COUNT(*) FROM access_tokens
WHERE (sqlc.arg('principal_id')::text = '' OR principal_id = sqlc.arg('principal_id')::text)
  AND (sqlc.arg('status')::bigint = 0
    OR ((sqlc.arg('status')::bigint = 1 OR sqlc.arg('status')::bigint = 2) AND revoked_at_ms IS NULL AND disabled_at_ms IS NULL AND expires_at_ms > sqlc.arg('at_ms')::bigint
      AND (sqlc.arg('status')::bigint = 1 OR expires_at_ms <= sqlc.arg('soon_ms')::bigint))
    OR (sqlc.arg('status')::bigint = 3 AND (revoked_at_ms IS NOT NULL OR disabled_at_ms IS NOT NULL OR expires_at_ms <= sqlc.arg('at_ms')::bigint)));

-- name: RevokeAccessToken :execrows
UPDATE access_tokens SET revoked_at_ms = sqlc.arg('revoked_at_ms')::bigint
WHERE id = sqlc.arg('id') AND revoked_at_ms IS NULL;

-- name: EndRotatedAccessToken :execrows
UPDATE access_tokens
SET expires_at_ms = LEAST(expires_at_ms, sqlc.arg('ends_at_ms')::bigint), replaced_by = sqlc.arg('replaced_by')::text
WHERE id = sqlc.arg('id') AND revoked_at_ms IS NULL AND disabled_at_ms IS NULL AND replaced_by IS NULL
  AND expires_at_ms > sqlc.arg('now_ms')::bigint;

-- name: TouchAccessToken :exec
UPDATE access_tokens SET last_used_at_ms = sqlc.arg('at_ms')::bigint
WHERE id = sqlc.arg('id') AND (last_used_at_ms IS NULL OR last_used_at_ms <= sqlc.arg('before_ms')::bigint);

-- name: GetDeletedStack :one
-- A deleted stack of one namespace by its stable ID (ADR 47), for a restore
-- or a purge. A live stack matches no row.
SELECT project, deleted_name
FROM stacks
WHERE namespace = $1 AND id = $2 AND deleted_at_ms IS NOT NULL;

-- name: RestoreStack :execrows
-- Brings a deleted stack back under name. The unique key on (namespace,
-- project, name) refuses a name that a live stack holds.
UPDATE stacks
SET name = $1, deleted_name = NULL, deleted_at_ms = NULL, updated_at_ms = $2
WHERE namespace = $3 AND id = $4 AND deleted_at_ms IS NOT NULL;

-- name: PurgeStack :execrows
-- The final delete of a deleted stack by its stable ID (ADR 47). The
-- children go first, through the queries below.
DELETE FROM stacks
WHERE namespace = $1 AND id = $2 AND deleted_at_ms IS NOT NULL;

-- name: DeleteEventsOfStack :exec
DELETE FROM update_events
WHERE stack_id = $1;

-- name: DeleteCheckpointsOfStack :exec
DELETE FROM checkpoints
WHERE stack_id = $1;

-- name: DeleteJournalSequencesOfStack :exec
DELETE FROM update_journal_sequences
WHERE stack_id = $1;

-- name: DeleteJournalOfStack :exec
DELETE FROM update_journal
WHERE stack_id = $1;

-- name: DeleteUpdatesOfStack :exec
DELETE FROM updates
WHERE stack_id = $1;

-- name: GetDeletedWorkspace :one
-- A deleted workspace of one namespace by its ID (ADR 47), for a restore or
-- a purge. A live workspace matches no row.
SELECT deleted_name
FROM workspaces
WHERE namespace = $1 AND id = $2 AND deleted_at_ms IS NOT NULL;

-- name: RestoreWorkspace :execrows
-- Brings a deleted workspace back under name. The revision moves on, as
-- the delete moved it. The unique key on (namespace, name) refuses a name
-- that a live workspace holds.
UPDATE workspaces
SET name = $1, deleted_name = NULL, deleted_at_ms = NULL, revision = revision + 1, updated_at_ms = $2
WHERE namespace = $3 AND id = $4 AND deleted_at_ms IS NOT NULL;

-- name: PurgeWorkspace :execrows
-- The final delete of a deleted workspace by its ID (ADR 47). The state
-- versions and the data key go first.
DELETE FROM workspaces
WHERE namespace = $1 AND id = $2 AND deleted_at_ms IS NOT NULL;

-- name: GetDeletedInventory :one
SELECT namespace, tool, unit_id, name, deleted_at_ms, purge_after_ms
FROM inventory_deleted
WHERE namespace = $1 AND tool = $2 AND unit_id = $3;

-- name: DeleteDeletedInventory :execrows
DELETE FROM inventory_deleted
WHERE tool = $1 AND unit_id = $2;

-- name: RevokeGrantsOnTarget :execrows
-- Ends every current grant on one target, for example unit:<stable ID> when
-- the purge of a deleted unit removes it (ADR 47). The rows stay for the
-- access-review history (ADR 52, decision 10).
UPDATE grants SET revoked_at_ms = $1, revoked_by = $2
WHERE target = $3 AND revoked_at_ms IS NULL;

-- name: InsertBlobPurge :exec
-- Records a purged unit whose blobs may still exist (ADR 47). A second
-- purge of the same unit cannot happen, but the row stays as it is.
INSERT INTO blob_purges (tool, unit_id, namespace, purged_at_ms)
VALUES ($1, $2, $3, $4)
ON CONFLICT (tool, unit_id) DO NOTHING;

-- name: DeleteBlobPurge :exec
DELETE FROM blob_purges WHERE tool = $1 AND unit_id = $2;

-- name: ListBlobPurges :many
-- The oldest rows first, so the sweep finishes the oldest purges first.
SELECT tool, unit_id, namespace, purged_at_ms
FROM blob_purges
ORDER BY purged_at_ms, tool COLLATE "C", unit_id COLLATE "C"
LIMIT $1;

-- name: GetAuditSinkCursor :one
-- The highest sequence of a namespace that a log sink holds (ADR 60
-- decision 13). A sink with no row holds no entry of the namespace.
SELECT sequence FROM audit_sink_cursors
WHERE sink = $1 AND namespace = $2;

-- name: AdvanceAuditSinkCursor :exec
-- Moves the cursor of a sink in a namespace up to a sequence. A cursor that
-- is already at the sequence or higher stays, so two instances that ship
-- the same batch never move it back.
INSERT INTO audit_sink_cursors (sink, namespace, sequence, updated_at_ms)
VALUES ($1, $2, $3, $4)
ON CONFLICT (sink, namespace) DO UPDATE
SET sequence = EXCLUDED.sequence, updated_at_ms = EXCLUDED.updated_at_ms
WHERE EXCLUDED.sequence > audit_sink_cursors.sequence;

-- Token policy and the inactivity disable (ADR 53).

-- name: GetAccessTokenPolicy :one
SELECT max_lifetime_ms, inactivity_ms, disable_tokens, disable_accounts, disable_break_glass, updated_at_ms
FROM access_token_policy WHERE id = 1;

-- name: SetAccessTokenPolicy :execrows
UPDATE access_token_policy
SET max_lifetime_ms = sqlc.arg('max_lifetime_ms')::bigint, inactivity_ms = sqlc.arg('inactivity_ms')::bigint,
  disable_tokens = sqlc.arg('disable_tokens')::boolean, disable_accounts = sqlc.arg('disable_accounts')::boolean,
  disable_break_glass = sqlc.arg('disable_break_glass')::boolean, updated_at_ms = sqlc.arg('updated_at_ms')::bigint
WHERE id = 1;

-- name: DatabaseNowMs :one
-- The time of the database clock in Unix milliseconds. The sweep uses it, so
-- a wrong clock on one instance cannot disable every record.
SELECT FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint AS now_ms;

-- name: DisableInactiveAccessTokens :many
UPDATE access_tokens SET disabled_at_ms = sqlc.arg('at_ms')::bigint
WHERE revoked_at_ms IS NULL AND disabled_at_ms IS NULL AND expires_at_ms > sqlc.arg('at_ms')::bigint
  AND COALESCE(last_used_at_ms, created_at_ms) <= sqlc.arg('cutoff_ms')::bigint
RETURNING id, principal_id;

-- name: DisableInactivePrincipals :many
-- A person is inactive when its creation, its activity baseline, and the
-- creation and the last use of each of its tokens are all at or before the
-- cutoff.
UPDATE principals SET disabled_at_ms = sqlc.arg('at_ms')::bigint
WHERE kind = 'person' AND deleted_at_ms IS NULL AND disabled_at_ms IS NULL
  AND created_at_ms <= sqlc.arg('cutoff_ms')::bigint
  AND last_active_at_ms <= sqlc.arg('cutoff_ms')::bigint
  AND NOT EXISTS (
    SELECT 1 FROM access_tokens t
    WHERE t.principal_id = principals.id
      AND (t.created_at_ms > sqlc.arg('cutoff_ms')::bigint OR t.last_used_at_ms > sqlc.arg('cutoff_ms')::bigint)
  )
RETURNING id;

-- name: EnablePrincipal :execrows
-- An enable also sets the activity baseline from the database clock, the
-- clock of the sweep, so the next sweep does not disable the principal again.
UPDATE principals SET disabled_at_ms = NULL, updated_at_ms = sqlc.arg('updated_at_ms')::bigint,
  last_active_at_ms = FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint
WHERE id = sqlc.arg('id') AND deleted_at_ms IS NULL AND disabled_at_ms IS NOT NULL;
