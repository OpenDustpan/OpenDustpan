-- name: CreateNamespace :execrows
INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
VALUES (?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetNamespace :one
SELECT name, home_region, failover_version, created_at_ms
FROM namespaces
WHERE name = ?;

-- name: ListNamespaces :many
SELECT name, home_region, failover_version, created_at_ms
FROM namespaces
ORDER BY name;

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
WHERE namespace = ?
ORDER BY project, name;

-- name: ListStacksInProject :many
SELECT namespace, project, name, id, version, created_at_ms, updated_at_ms
FROM stacks
WHERE namespace = ? AND project = ?
ORDER BY name;

-- name: CreateStackTag :exec
INSERT INTO stack_tags (stack_id, name, value)
VALUES (?, ?, ?);

-- name: GetStackTags :many
SELECT name, value
FROM stack_tags
WHERE stack_id = ?
ORDER BY name;

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
    lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetUpdate :one
SELECT stack_id, id, kind, dry_run, status, version, revision,
       lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
FROM updates
WHERE stack_id = ? AND id = ?;

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
SELECT stack_id, id, kind, dry_run, status, version, revision,
       lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
FROM updates
WHERE stack_id = ?
  AND status = 'running' AND dry_run = 0 AND kind <> 'preview';

-- name: ListUpdates :many
SELECT stack_id, id, kind, dry_run, status, version, revision,
       lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
FROM updates
WHERE stack_id = ?
  AND status <> 'not started' AND dry_run = 0 AND kind <> 'preview'
ORDER BY version DESC
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
       c.digest, c.invalid, c.features_json, c.deployment, c.saved_at_ms
FROM checkpoints AS c
JOIN updates AS u ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE c.stack_id = ?
ORDER BY u.version DESC
LIMIT 1;

-- name: GetCheckpointByUpdate :one
SELECT stack_id, update_id, lease_digest, schema_version, sequence,
       digest, invalid, features_json, deployment, saved_at_ms
FROM checkpoints
WHERE stack_id = ? AND update_id = ?;

-- name: GetCheckpointByVersion :one
SELECT c.stack_id, c.update_id, c.lease_digest, c.schema_version, c.sequence,
       c.digest, c.invalid, c.features_json, c.deployment, c.saved_at_ms
FROM checkpoints AS c
JOIN updates AS u ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE c.stack_id = ? AND u.version = ?;

-- name: PutCheckpoint :exec
INSERT INTO checkpoints (
    stack_id, update_id, lease_digest, schema_version, sequence,
    digest, invalid, features_json, deployment, saved_at_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (stack_id, update_id) DO UPDATE SET
    lease_digest = excluded.lease_digest,
    schema_version = excluded.schema_version,
    sequence = excluded.sequence,
    digest = excluded.digest,
    invalid = excluded.invalid,
    features_json = excluded.features_json,
    deployment = excluded.deployment,
    saved_at_ms = excluded.saved_at_ms;

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
INSERT INTO inventory (namespace, tool, name, unit_id, display_name, last_changed_ms, status, resource_count)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (namespace, tool, name) DO UPDATE SET
    unit_id = excluded.unit_id,
    display_name = excluded.display_name,
    last_changed_ms = excluded.last_changed_ms,
    status = excluded.status,
    resource_count = excluded.resource_count;

-- name: DeleteInventory :exec
DELETE FROM inventory
WHERE namespace = ? AND tool = ? AND name = ?;

-- name: ListInventory :many
SELECT namespace, tool, name, unit_id, display_name, last_changed_ms, status, resource_count
FROM inventory
WHERE namespace = ?
ORDER BY tool, name;

-- name: CreateWorkspace :execrows
INSERT INTO workspaces (
    id, namespace, name, revision, lock_id, lock_holder, lock_reason,
    locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetWorkspace :one
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms
FROM workspaces
WHERE namespace = ? AND name = ?;

-- name: GetWorkspaceByID :one
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms
FROM workspaces
WHERE id = ?;

-- name: ListWorkspaces :many
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms
FROM workspaces
WHERE namespace = ?
ORDER BY name;

-- name: SaveWorkspace :execrows
-- A compare-and-swap: it changes nothing unless the stored revision is the
-- one the caller read. The current state pointer stays: only FinalizeStateVersion moves it.
UPDATE workspaces
SET revision = @revision, lock_id = @lock_id, lock_holder = @lock_holder,
    lock_reason = @lock_reason, locked_at_ms = @locked_at_ms, updated_at_ms = @updated_at_ms
WHERE id = @id AND revision = @expected_revision;

-- name: CreateStateVersion :exec
INSERT INTO state_versions (
    id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
    force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
    restored_from
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetStateVersion :one
SELECT id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
       force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
       restored_from
FROM state_versions
WHERE id = ?;

-- name: ListStateVersions :many
-- Finalized versions of one workspace, newest first. Ties on finalize time
-- break by ID, as the port comment promises. The caller normalizes the page.
SELECT id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
       force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
       restored_from
FROM state_versions
WHERE workspace_id = ? AND status = 'finalized'
ORDER BY finalized_at_ms DESC, id ASC
LIMIT ? OFFSET ?;

-- name: CountStateVersions :one
SELECT COUNT(*)
FROM state_versions
WHERE workspace_id = ? AND status = 'finalized';

-- name: FinalizeStateVersion :execrows
-- Marks one pending version finalized with its content. A finalized version
-- matches no row, so a retry reports no change.
UPDATE state_versions
SET status = @status, finalized_at_ms = @finalized_at_ms,
    content_key = @content_key, size = @size
WHERE id = @id AND status = 'pending';

-- name: SetWorkspaceCurrent :exec
UPDATE workspaces
SET current_state_version_id = ?, updated_at_ms = ?
WHERE id = ?;

-- name: GetCurrentStateVersion :one
-- Follows the workspace pointer: a forced upload may carry a lower serial and
-- still be current, so the highest serial is not always the current state.
SELECT sv.id, sv.workspace_id, sv.status, sv.serial, sv.lineage, sv.md5, sv.size,
       sv.content_key, sv.outputs, sv.force, sv.upload_digest, sv.upload_expires_at_ms,
       sv.holder, sv.lock_id, sv.created_at_ms, sv.finalized_at_ms, sv.restored_from
FROM state_versions AS sv
JOIN workspaces AS w ON w.current_state_version_id = sv.id
WHERE w.id = ?;
