-- name: CreateNamespace :execrows
INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING;

-- name: GetNamespace :one
SELECT name, home_region, failover_version, created_at_ms
FROM namespaces
WHERE name = $1;

-- name: ListNamespaces :many
SELECT name, home_region, failover_version, created_at_ms
FROM namespaces
ORDER BY name;

-- name: GetFailoverVersionForShare :one
-- Locks the namespace row so a concurrent promotion waits for this write.
SELECT failover_version
FROM namespaces
WHERE name = $1
FOR SHARE;

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
WHERE namespace = $1
ORDER BY project, name;

-- name: ListStacksInProject :many
SELECT namespace, project, name, id, version, created_at_ms, updated_at_ms
FROM stacks
WHERE namespace = $1 AND project = $2
ORDER BY name;

-- name: CreateStackTag :exec
INSERT INTO stack_tags (stack_id, name, value)
VALUES ($1, $2, $3);

-- name: GetStackTags :many
SELECT name, value
FROM stack_tags
WHERE stack_id = $1
ORDER BY name;

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
       u.created_by, u.restored_from,
       COALESCE(octet_length(c.deployment), 0)::BIGINT AS checkpoint_bytes
FROM updates AS u
LEFT JOIN checkpoints AS c ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE u.stack_id = $1 AND u.id = $2;

-- name: SaveUpdate :execrows
-- A compare-and-swap: it changes nothing unless the stored revision is the
-- one the caller read.
UPDATE updates
SET status = @status, version = @version, revision = @revision,
    lease_digest = @lease_digest, lease_expires_at_ms = @lease_expires_at_ms,
    started_at_ms = @started_at_ms, ended_at_ms = @ended_at_ms
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
       u.created_by, u.restored_from,
       COALESCE(octet_length(c.deployment), 0)::BIGINT AS checkpoint_bytes
FROM updates AS u
LEFT JOIN checkpoints AS c ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE u.stack_id = $1
  AND u.status = 'running' AND NOT u.dry_run AND u.kind <> 'preview';

-- name: ListUpdates :many
SELECT u.stack_id, u.id, u.kind, u.dry_run, u.status, u.version, u.revision,
       u.lease_digest, u.lease_expires_at_ms, u.created_at_ms, u.started_at_ms, u.ended_at_ms,
       u.created_by, u.restored_from,
       COALESCE(octet_length(c.deployment), 0)::BIGINT AS checkpoint_bytes
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
       c.digest, c.invalid, c.features, c.deployment, c.saved_at_ms
FROM checkpoints AS c
JOIN updates AS u ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE c.stack_id = $1
ORDER BY u.version DESC
LIMIT 1;

-- name: GetCheckpointByUpdate :one
SELECT stack_id, update_id, lease_digest, schema_version, sequence,
       digest, invalid, features, deployment, saved_at_ms
FROM checkpoints
WHERE stack_id = $1 AND update_id = $2;

-- name: GetCheckpointByVersion :one
SELECT c.stack_id, c.update_id, c.lease_digest, c.schema_version, c.sequence,
       c.digest, c.invalid, c.features, c.deployment, c.saved_at_ms
FROM checkpoints AS c
JOIN updates AS u ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE c.stack_id = $1 AND u.version = $2;

-- name: PutCheckpoint :exec
INSERT INTO checkpoints (
    stack_id, update_id, lease_digest, schema_version, sequence,
    digest, invalid, features, deployment, saved_at_ms
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (stack_id, update_id) DO UPDATE SET
    lease_digest = excluded.lease_digest,
    schema_version = excluded.schema_version,
    sequence = excluded.sequence,
    digest = excluded.digest,
    invalid = excluded.invalid,
    features = excluded.features,
    deployment = excluded.deployment,
    saved_at_ms = excluded.saved_at_ms;

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
INSERT INTO inventory (namespace, tool, name, unit_id, display_name, last_changed_ms, status, resource_count)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (namespace, tool, name) DO UPDATE SET
    unit_id = excluded.unit_id,
    display_name = excluded.display_name,
    last_changed_ms = excluded.last_changed_ms,
    status = excluded.status,
    resource_count = excluded.resource_count;

-- name: DeleteInventory :exec
DELETE FROM inventory
WHERE namespace = $1 AND tool = $2 AND name = $3;

-- name: ListInventory :many
SELECT namespace, tool, name, unit_id, display_name, last_changed_ms, status, resource_count
FROM inventory
WHERE namespace = $1
ORDER BY tool, name;

-- name: CreateWorkspace :execrows
INSERT INTO workspaces (
    id, namespace, name, revision, lock_id, lock_holder, lock_reason,
    locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT DO NOTHING;

-- name: GetWorkspace :one
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms
FROM workspaces
WHERE namespace = $1 AND name = $2;

-- name: GetWorkspaceByID :one
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms
FROM workspaces
WHERE id = $1;

-- name: LockWorkspaceForFinalize :one
-- Locks the workspace row so two finalizes on one workspace cannot both pass
-- the serial check.
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms
FROM workspaces
WHERE id = $1
FOR UPDATE;

-- name: ListWorkspaces :many
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms
FROM workspaces
WHERE namespace = $1
ORDER BY name;

-- name: SaveWorkspace :execrows
-- A compare-and-swap: it changes nothing unless the stored revision is the
-- one the caller read. The current state pointer stays: only FinalizeStateVersion moves it.
UPDATE workspaces
SET revision = @revision, lock_id = @lock_id, lock_holder = @lock_holder,
    lock_reason = @lock_reason, locked_at_ms = @locked_at_ms, updated_at_ms = @updated_at_ms
WHERE id = @id AND revision = @expected_revision;

-- name: WorkspaceExists :one
SELECT EXISTS (
    SELECT 1 FROM workspaces WHERE id = $1
);

-- name: CreateStateVersion :exec
INSERT INTO state_versions (
    id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
    force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
    restored_from
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
);

-- name: GetStateVersion :one
SELECT id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
       force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
       restored_from
FROM state_versions
WHERE id = $1;

-- name: ListStateVersions :many
-- Finalized versions of one workspace, newest first. Ties on finalize time
-- break by ID, as the port comment promises. The caller normalizes the page.
SELECT id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
       force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
       restored_from
FROM state_versions
WHERE workspace_id = $1 AND status = 'finalized'
ORDER BY finalized_at_ms DESC, id ASC
LIMIT $2 OFFSET $3;

-- name: CountStateVersions :one
SELECT COUNT(*)
FROM state_versions
WHERE workspace_id = $1 AND status = 'finalized';

-- name: GetCurrentStateVersion :one
-- Follows the workspace pointer: a forced upload may carry a lower serial and
-- still be current, so the highest serial is not always the current state.
SELECT sv.id, sv.workspace_id, sv.status, sv.serial, sv.lineage, sv.md5, sv.size,
       sv.content_key, sv.outputs, sv.force, sv.upload_digest, sv.upload_expires_at_ms,
       sv.holder, sv.lock_id, sv.created_at_ms, sv.finalized_at_ms, sv.restored_from
FROM state_versions AS sv
JOIN workspaces AS w ON w.current_state_version_id = sv.id
WHERE w.id = $1;

-- name: FinalizeStateVersion :execrows
-- Marks one pending version finalized with its content. A finalized version
-- matches no row, so a retry reports no change.
UPDATE state_versions
SET status = 'finalized', finalized_at_ms = @finalized_at_ms,
    content_key = @content_key, size = @size
WHERE id = @id AND status = 'pending';

-- name: SetWorkspaceCurrent :exec
UPDATE workspaces
SET current_state_version_id = $1, updated_at_ms = $2
WHERE id = $3;
