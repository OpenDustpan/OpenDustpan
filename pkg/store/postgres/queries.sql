-- name: CreateNamespace :execrows
INSERT INTO namespaces (name, home_region, failover_version, created_at_ms, id)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT DO NOTHING;

-- name: GetNamespace :one
SELECT name, home_region, failover_version, created_at_ms, allow_forced_writes, allow_unlocked_writes, id
FROM namespaces
WHERE name = $1;

-- name: ListNamespaces :many
SELECT name, home_region, failover_version, created_at_ms, allow_forced_writes, allow_unlocked_writes, id
FROM namespaces
ORDER BY name;

-- name: PageNamespaces :many
SELECT name, home_region, failover_version, created_at_ms, allow_forced_writes, allow_unlocked_writes, id
FROM namespaces
WHERE name > sqlc.arg('after')
ORDER BY name
LIMIT sqlc.arg('lim');

-- name: PageNamespacesByNames :many
SELECT name, home_region, failover_version, created_at_ms, allow_forced_writes, allow_unlocked_writes, id
FROM namespaces
WHERE name > sqlc.arg('after') AND name = ANY(sqlc.arg('names')::text[])
ORDER BY name
LIMIT sqlc.arg('lim');

-- name: CountNamespaces :one
SELECT COUNT(*)
FROM namespaces;

-- name: CountNamespacesByNames :one
SELECT COUNT(*)
FROM namespaces
WHERE name = ANY(sqlc.arg('names')::text[]);

-- name: SetNamespaceOverrides :execrows
-- Replaces the override switches of one namespace (ADR 44, decision 6).
UPDATE namespaces
SET allow_forced_writes = $1, allow_unlocked_writes = $2
WHERE name = $3;

-- name: GetNamespaceIPAllowlist :one
SELECT ip_allowlist
FROM namespaces
WHERE name = $1;

-- name: SetNamespaceIPAllowlist :execrows
-- Replaces the IP allowlist of one namespace (ADR 55, decision 8).
UPDATE namespaces
SET ip_allowlist = $1
WHERE name = $2;

-- name: GetNamespaceForShare :one
-- Reads the namespace row with a shared lock. A switch change waits for the
-- transaction that reads it, so the switch it reads is the one that commits.
SELECT name, home_region, failover_version, created_at_ms, allow_forced_writes, allow_unlocked_writes, id
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

-- name: StackRefByID :one
SELECT namespace, project, name
FROM stacks
WHERE id = $1;

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
       COALESCE(c.size, 0)::BIGINT AS checkpoint_bytes
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

-- name: CountRunningJournaledUpdates :one
-- The count of updates that run now with journal version 1 or higher, for
-- the upgrade check: a downgrade to a binary without the journal route
-- answers its journal batches with 404, which the Pulumi CLI does not retry.
SELECT COUNT(*)
FROM updates
WHERE status = 'running' AND journal_version > 0;

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
       COALESCE(c.size, 0)::BIGINT AS checkpoint_bytes
FROM updates AS u
LEFT JOIN checkpoints AS c ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE u.stack_id = $1
  AND u.status = 'running' AND NOT u.dry_run AND u.kind <> 'preview';

-- name: ListUpdates :many
SELECT u.stack_id, u.id, u.kind, u.dry_run, u.status, u.version, u.revision,
       u.lease_digest, u.lease_expires_at_ms, u.created_at_ms, u.started_at_ms, u.ended_at_ms,
       u.created_by, u.restored_from, u.journal_version, u.token_id,
       COALESCE(c.size, 0)::BIGINT AS checkpoint_bytes
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
       c.digest, c.invalid, c.features, c.saved_at_ms,
       c.blob_key, c.size, c.blob_size, c.blob_md5
FROM checkpoints AS c
JOIN updates AS u ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE c.stack_id = $1
ORDER BY u.version DESC
LIMIT 1;

-- name: StackExistsByID :one
SELECT id FROM stacks WHERE id = $1;

-- name: StateAtUpdate :one
SELECT u.version, u.ended_at_ms, u.created_by
FROM updates AS u
JOIN checkpoints AS c ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE u.stack_id = $1
  AND u.dry_run = FALSE AND u.kind <> 'preview'
  AND u.status <> 'not started'
  AND u.ended_at_ms > 0 AND u.ended_at_ms <= $2
ORDER BY u.version DESC
LIMIT 1;

-- name: FirstStateUpdateEnd :one
SELECT u.ended_at_ms
FROM updates AS u
JOIN checkpoints AS c ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE u.stack_id = $1
  AND u.dry_run = FALSE AND u.kind <> 'preview'
  AND u.status <> 'not started'
  AND u.ended_at_ms > 0
ORDER BY u.version ASC
LIMIT 1;

-- name: RunningUpdateAt :one
SELECT u.version
FROM updates AS u
WHERE u.stack_id = $1
  AND u.version > $3
  AND u.dry_run = FALSE AND u.kind <> 'preview'
  AND u.status <> 'not started'
  AND u.started_at_ms <= $2
  AND (u.ended_at_ms = 0 OR u.ended_at_ms > $2)
ORDER BY u.version DESC
LIMIT 1;

-- name: GetCheckpointByUpdate :one
SELECT stack_id, update_id, lease_digest, schema_version, sequence,
       digest, invalid, features, saved_at_ms,
       blob_key, size, blob_size, blob_md5
FROM checkpoints
WHERE stack_id = $1 AND update_id = $2;

-- name: GetCheckpointByVersion :one
SELECT c.stack_id, c.update_id, c.lease_digest, c.schema_version, c.sequence,
       c.digest, c.invalid, c.features, c.saved_at_ms,
       c.blob_key, c.size, c.blob_size, c.blob_md5
FROM checkpoints AS c
JOIN updates AS u ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE c.stack_id = $1 AND u.version = $2;

-- name: PutCheckpoint :exec
INSERT INTO checkpoints (
    stack_id, update_id, lease_digest, schema_version, sequence,
    digest, invalid, features, saved_at_ms,
    blob_key, size, blob_size, blob_md5
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (stack_id, update_id) DO UPDATE SET
    lease_digest = excluded.lease_digest,
    schema_version = excluded.schema_version,
    sequence = excluded.sequence,
    digest = excluded.digest,
    invalid = excluded.invalid,
    features = excluded.features,
    saved_at_ms = excluded.saved_at_ms,
    blob_key = excluded.blob_key,
    size = excluded.size,
    blob_size = excluded.blob_size,
    blob_md5 = excluded.blob_md5;

-- name: ListCheckpointBlobKeys :many
-- Every blob key the checkpoints of one stack name, ordered. A sweeper
-- compares this set against the stored objects to find blobs no checkpoint
-- references.
SELECT blob_key
FROM checkpoints
WHERE stack_id = $1
ORDER BY blob_key;

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
-- ended at or before $1, for the retention sweep. Byte order
-- (COLLATE "C"), as inventory.PageExpired orders.
SELECT namespace, tool, unit_id, name, deleted_at_ms, purge_after_ms
FROM inventory_deleted
WHERE purge_after_ms <= $1
ORDER BY purge_after_ms, tool COLLATE "C", unit_id COLLATE "C"
LIMIT $2;

-- name: ListExpiredInventoryAfter :many
-- The page of ListExpiredInventory after the cursor (end of retention,
-- tool, unit ID). The lower bound on purge_after_ms lets the index scan
-- start at the cursor.
SELECT namespace, tool, unit_id, name, deleted_at_ms, purge_after_ms
FROM inventory_deleted
WHERE purge_after_ms <= sqlc.arg(now_ms)
  AND purge_after_ms >= sqlc.arg(after_ms)
  AND (purge_after_ms > sqlc.arg(after_ms)
    OR (purge_after_ms = sqlc.arg(after_ms) AND tool COLLATE "C" > sqlc.arg(after_tool))
    OR (purge_after_ms = sqlc.arg(after_ms) AND tool = sqlc.arg(after_tool)
      AND unit_id COLLATE "C" > sqlc.arg(after_unit_id)))
ORDER BY purge_after_ms, tool COLLATE "C", unit_id COLLATE "C"
LIMIT sqlc.arg(page_limit);

-- name: DeleteInventory :exec
DELETE FROM inventory
WHERE namespace = $1 AND tool = $2 AND name = $3;

-- name: NextInventoryChangeSeq :one
-- Takes the next number of the change log of one namespace. The statement runs
-- last in the transaction of the write, after every other row lock, so the
-- order of the numbers is the order of the commits of the namespace.
INSERT INTO inventory_change_heads (namespace_id, last_seq)
VALUES ($1, 1)
ON CONFLICT (namespace_id) DO UPDATE SET last_seq = inventory_change_heads.last_seq + 1
RETURNING last_seq;

-- name: InsertInventoryChange :exec
INSERT INTO inventory_changes (
    namespace_id, seq, at_ms, tool, unit_id, name, display_name, status, collection, kind, resource_count
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: GetUnitCollectionName :one
-- The name of the collection that holds the unit, for a change log row.
SELECT c.name
FROM collection_members AS m
JOIN collections AS c ON c.namespace = m.namespace AND c.name_key = m.collection_key
WHERE m.unit_id = $1;

-- name: ListInventoryChanges :many
-- One page of the change log of a namespace in the order of the sequence.
SELECT namespace_id, seq, at_ms, tool, unit_id, name, display_name, status, collection, kind, resource_count
FROM inventory_changes
WHERE namespace_id = sqlc.arg(namespace_id)
  AND at_ms <= sqlc.arg(until_ms)
  AND seq > sqlc.arg(after_seq)
ORDER BY seq
LIMIT sqlc.arg(page_limit);

-- name: ListInventoryChangeBounds :many
-- For each unit of a namespace, the row with the highest sequence among the
-- rows at or before the time, and the row with the highest sequence of all.
-- ReplayAt and ReplayView need no other row. A skip scan on the index finds
-- each unit once, then two lookups on the index read its rows. The lookup at
-- the time reads the rows of the unit after the time, so the cost follows the
-- count of units and of the rows after the time.
WITH RECURSIVE units(tool, unit_id) AS (
    (SELECT f.tool, f.unit_id
     FROM inventory_changes AS f
     WHERE f.namespace_id = sqlc.arg(namespace_id)
     ORDER BY f.tool, f.unit_id
     LIMIT 1)
    UNION ALL
    SELECT n.tool, n.unit_id
    FROM units AS u
    CROSS JOIN LATERAL (
        SELECT s.tool, s.unit_id
        FROM inventory_changes AS s
        WHERE s.namespace_id = sqlc.arg(namespace_id) AND (s.tool, s.unit_id) > (u.tool, u.unit_id)
        ORDER BY s.tool, s.unit_id
        LIMIT 1
    ) AS n
)
SELECT r.namespace_id, r.seq, r.at_ms, r.tool, r.unit_id, r.name, r.display_name, r.status, r.collection,
       r.kind, r.resource_count
FROM units AS u
CROSS JOIN LATERAL (
    (SELECT a.namespace_id, a.seq, a.at_ms, a.tool, a.unit_id, a.name, a.display_name, a.status, a.collection,
            a.kind, a.resource_count
     FROM inventory_changes AS a
     WHERE a.namespace_id = sqlc.arg(namespace_id) AND a.tool = u.tool AND a.unit_id = u.unit_id
       AND a.at_ms <= sqlc.arg(at_ms)
     ORDER BY a.seq DESC
     LIMIT 1)
    UNION
    (SELECT l.namespace_id, l.seq, l.at_ms, l.tool, l.unit_id, l.name, l.display_name, l.status, l.collection,
            l.kind, l.resource_count
     FROM inventory_changes AS l
     WHERE l.namespace_id = sqlc.arg(namespace_id) AND l.tool = u.tool AND l.unit_id = u.unit_id
     ORDER BY l.seq DESC
     LIMIT 1)
) AS r
ORDER BY r.seq;

-- name: ListInventoryUnitBounds :many
-- The rows of ListInventoryChangeBounds for one unit.
(SELECT a.namespace_id, a.seq, a.at_ms, a.tool, a.unit_id, a.name, a.display_name, a.status, a.collection,
        a.kind, a.resource_count
 FROM inventory_changes AS a
 WHERE a.namespace_id = sqlc.arg(namespace_id) AND a.tool = sqlc.arg(tool) AND a.unit_id = sqlc.arg(unit_id)
   AND a.at_ms <= sqlc.arg(at_ms)
 ORDER BY a.seq DESC
 LIMIT 1)
UNION
(SELECT l.namespace_id, l.seq, l.at_ms, l.tool, l.unit_id, l.name, l.display_name, l.status, l.collection,
        l.kind, l.resource_count
 FROM inventory_changes AS l
 WHERE l.namespace_id = sqlc.arg(namespace_id) AND l.tool = sqlc.arg(tool) AND l.unit_id = sqlc.arg(unit_id)
 ORDER BY l.seq DESC
 LIMIT 1)
ORDER BY seq;

-- name: ListInventory :many
SELECT i.namespace, i.tool, i.name, i.unit_id, i.display_name, i.last_changed_ms, i.status, i.resource_count,
       i.lock_holder, i.lock_since_ms, i.lock_operation,
       COALESCE(c.name, '') AS collection
FROM inventory AS i
LEFT JOIN collection_members AS m ON m.unit_id = i.unit_id
LEFT JOIN collections AS c ON c.namespace = m.namespace AND c.name_key = m.collection_key
WHERE i.namespace = $1
ORDER BY i.tool, i.name;

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

-- name: WorkspaceExistsByID :one
-- A workspace by its ID, a soft-deleted one included.
SELECT id FROM workspaces WHERE id = $1;

-- name: WorkspaceStateAt :one
-- The finalized state version with the latest finalize time at or before a
-- time. A finalize stamps a time after every earlier finalize of the
-- workspace, so the version that became current last has the latest finalize
-- time, and a read at the present agrees with the current pointer. Ties break
-- by ID, as in the state version list; only rows that the stamp did not write
-- can tie. The index on (workspace_id, finalized_at_ms DESC) serves the order.
SELECT id, finalized_at_ms, holder
FROM state_versions
WHERE workspace_id = $1 AND status = 'finalized' AND finalized_at_ms <= $2
ORDER BY finalized_at_ms DESC, id ASC
LIMIT 1;

-- name: LatestWorkspaceStateFinalize :one
-- The latest finalize time of the finalized state versions of a workspace. A
-- finalize stamps a later time than this one, in the same transaction.
SELECT finalized_at_ms
FROM state_versions
WHERE workspace_id = $1 AND status = 'finalized'
ORDER BY finalized_at_ms DESC
LIMIT 1;

-- name: FirstWorkspaceStateFinalize :one
-- The finalize time of the first finalized state version of a workspace.
SELECT finalized_at_ms
FROM state_versions
WHERE workspace_id = $1 AND status = 'finalized'
ORDER BY finalized_at_ms ASC, id ASC
LIMIT 1;

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
-- The installation state: normal or maintenance. The migration seeds the
-- one row; a missing row is a fault.
SELECT state
FROM installation
WHERE id = 1;

-- name: SetInstallationState :execrows
-- Stores the installation state in the seeded row. It changes nothing when
-- the row is missing.
UPDATE installation
SET state = $1
WHERE id = 1;

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
-- The wrapper IDs of the namespace keys and of the installation keys, so the
-- restore check finds the key-encryption key of each.
SELECT wrapper_id FROM secret_keys
UNION
SELECT wrapper_id FROM installation_secret_keys
ORDER BY wrapper_id;

-- name: BackupBlobs :many
-- Every blob key the finalized state versions and the checkpoints name, with the size
-- and MD5 of the plain bytes and the sealed flag, ordered by key. Sealed
-- state blobs are stored sealed: the backup opens them to check and stores
-- the sealed bytes. A pending version has no committed content, so the backup
-- leaves pending rows out.
SELECT content_key, size, md5, sealed
FROM state_versions
WHERE content_key <> '' AND status = 'finalized'
UNION ALL
SELECT blob_key AS content_key, blob_size AS size, blob_md5 AS md5, FALSE AS sealed
FROM checkpoints
UNION ALL
SELECT blob_key AS content_key, size AS size, md5 AS md5, FALSE AS sealed
FROM update_journal
ORDER BY content_key;

-- name: GetAuditEntry :one
-- The entry of a scope at one sequence, for the check of signed
-- checkpoints. A missing entry matches no row.
SELECT namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
       at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count, changes, object_id,
       fence_version
FROM audit_entries
WHERE namespace = $1 AND sequence = $2;

-- name: LastAuditEntry :one
-- The entry with the highest sequence of a namespace, for the next append.
-- A namespace with no entries matches no row.
SELECT namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
       at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count, changes, object_id,
       fence_version
FROM audit_entries
WHERE namespace = $1
ORDER BY sequence DESC
LIMIT 1;

-- name: InsertAuditEntry :exec
INSERT INTO audit_entries (
    namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
    at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count, changes, object_id,
    fence_version
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22);

-- name: ListAuditEntries :many
-- One page of the entries of a namespace, newest first. Ties on time break
-- by sequence, as the port comment promises.
SELECT namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
       at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count, changes, object_id,
       fence_version
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
       at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count, changes, object_id,
       fence_version
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

-- name: ClearJournalOperations :exec
-- A batch that holds a rebuilt base state entry starts the known set again:
-- earlier batches keep their rows for the replay, but their operation IDs no
-- longer count.
UPDATE update_journal SET operation_ids = '[]'
WHERE stack_id = $1 AND update_id = $2;

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

-- name: NextRefusalArrival :one
SELECT (COALESCE(MAX(arrival), 0) + 1)::BIGINT AS arrival
FROM update_journal_refusals
WHERE stack_id = $1 AND update_id = $2;

-- name: InsertJournalRefusal :exec
INSERT INTO update_journal_refusals (stack_id, update_id, arrival, sequence_ids, status, reason)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListJournalRefusals :many
SELECT sequence_ids, status, reason FROM update_journal_refusals
WHERE stack_id = $1 AND update_id = $2
ORDER BY arrival;

-- name: ListJournalSequences :many
SELECT sequence FROM update_journal_sequences
WHERE stack_id = $1 AND update_id = $2
ORDER BY sequence;

-- name: DeleteJournalRefusals :exec
DELETE FROM update_journal_refusals
WHERE stack_id = $1 AND update_id = $2;

-- name: JournalState :one
SELECT journal_closed, journal_version, journal_records_changed, status FROM updates
WHERE stack_id = $1 AND id = $2;

-- name: SetJournalRecordsChanged :exec
UPDATE updates SET journal_records_changed = TRUE
WHERE stack_id = $1 AND id = $2;

-- name: CloseJournal :exec
UPDATE updates SET journal_closed = TRUE
WHERE stack_id = $1 AND id = $2;

-- name: CreatePrincipal :execrows
INSERT INTO principals (id, subject, display_name, kind, source, created_at_ms, updated_at_ms, namespace, break_glass)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT DO NOTHING;

-- name: GetPrincipal :one
SELECT p.id, p.subject, p.display_name, p.kind, p.source, p.created_at_ms, p.updated_at_ms, p.disabled_at_ms, p.namespace,
  p.break_glass,
  (SELECT COUNT(*) FROM group_members m WHERE m.principal_id = p.id AND m.removed_at_ms IS NULL AND m.state = 'active') AS group_count
FROM principals p WHERE p.id = $1 AND p.deleted_at_ms IS NULL;

-- name: GetPrincipalBySubject :one
SELECT p.id, p.subject, p.display_name, p.kind, p.source, p.created_at_ms, p.updated_at_ms, p.disabled_at_ms, p.namespace,
  p.break_glass,
  (SELECT COUNT(*) FROM group_members m WHERE m.principal_id = p.id AND m.removed_at_ms IS NULL AND m.state = 'active') AS group_count
FROM principals p WHERE p.subject COLLATE "C" = @subject::text AND p.deleted_at_ms IS NULL;

-- name: ListPrincipals :many
SELECT p.id, p.subject, p.display_name, p.kind, p.source, p.created_at_ms, p.updated_at_ms, p.disabled_at_ms, p.namespace,
  p.break_glass,
  (SELECT COUNT(*) FROM group_members m WHERE m.principal_id = p.id AND m.removed_at_ms IS NULL AND m.state = 'active') AS group_count
FROM principals p
WHERE p.deleted_at_ms IS NULL
  AND p.subject COLLATE "C" > @after::text
  AND p.subject COLLATE "C" >= @low::text
  AND (@high::text = '' OR p.subject COLLATE "C" < @high::text)
  AND (@kind::text = '' OR p.kind = @kind::text)
  AND (@namespace::text = '' OR p.namespace = @namespace::text)
ORDER BY p.subject COLLATE "C"
LIMIT @row_limit;

-- name: CountPrincipals :one
SELECT COUNT(*) FROM principals
WHERE deleted_at_ms IS NULL
  AND subject COLLATE "C" >= @low::text
  AND (@high::text = '' OR subject COLLATE "C" < @high::text)
  AND (@kind::text = '' OR kind = @kind::text)
  AND (@namespace::text = '' OR namespace = @namespace::text);

-- name: GetPrincipalNamespace :one
-- The namespace of a current principal: empty for a principal of the
-- installation. A grant write reads it in its own transaction.
SELECT namespace FROM principals WHERE id = $1 AND deleted_at_ms IS NULL;

-- name: UpdatePrincipal :execrows
UPDATE principals SET display_name = $1, updated_at_ms = $2
WHERE id = $3 AND namespace = $4 AND deleted_at_ms IS NULL;

-- name: DeletePrincipal :execrows
UPDATE principals SET deleted_at_ms = $1
WHERE id = $2 AND namespace = $3 AND deleted_at_ms IS NULL;

-- name: EndMembershipsOfPrincipal :exec
UPDATE group_members SET removed_at_ms = $1
WHERE principal_id = $2 AND removed_at_ms IS NULL;

-- name: CreateGroup :execrows
INSERT INTO principal_groups (id, name, name_key, description, source, created_at_ms, updated_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT DO NOTHING;

-- name: GetGroup :one
SELECT g.id, g.name, g.description, g.source, g.created_at_ms, g.updated_at_ms,
  (SELECT COUNT(*) FROM group_members m WHERE m.group_id = g.id AND m.removed_at_ms IS NULL AND m.state = 'active') AS member_count
FROM principal_groups g WHERE g.id = $1 AND g.deleted_at_ms IS NULL;

-- name: GroupIDByNameKey :one
SELECT id FROM principal_groups WHERE name_key COLLATE "C" = @name_key::text AND deleted_at_ms IS NULL;

-- name: ListGroups :many
SELECT g.id, g.name, g.description, g.source, g.created_at_ms, g.updated_at_ms,
  (SELECT COUNT(*) FROM group_members m WHERE m.group_id = g.id AND m.removed_at_ms IS NULL AND m.state = 'active') AS member_count
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
INSERT INTO group_members (group_id, principal_id, added_at_ms, source, source_id, state, suspended_at_ms, pinned)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT DO NOTHING;

-- name: RemoveGroupMember :execrows
UPDATE group_members SET removed_at_ms = $1
WHERE group_id = $2 AND principal_id = $3 AND removed_at_ms IS NULL;

-- name: ListGroupMembers :many
SELECT p.id, p.subject, p.display_name, p.kind, p.source, p.created_at_ms, p.updated_at_ms, p.disabled_at_ms, p.namespace,
  p.break_glass,
  (SELECT COUNT(*) FROM group_members o WHERE o.principal_id = p.id AND o.removed_at_ms IS NULL AND o.state = 'active') AS group_count,
  m.added_at_ms, m.source AS member_source, m.source_id AS member_source_id, m.state AS member_state,
  m.suspended_at_ms AS member_suspended_at_ms, m.pinned AS member_pinned
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
  (SELECT COUNT(*) FROM group_members o WHERE o.group_id = g.id AND o.removed_at_ms IS NULL AND o.state = 'active') AS member_count
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
-- One page of the current grants on a target, with the name and the kind of
-- each holder: the display name (else the subject) and the kind of a
-- principal, or the name of a group and the kind "group". An empty subject
-- or role filter keeps every grant.
SELECT g.id, g.subject, g.subject_kind, g.subject_id, g.role, g.object, g.target, g.object_kind, g.namespace,
  g.condition, g.created_at_ms, g.created_by,
  COALESCE(NULLIF(p.display_name, ''), p.subject, pg.name, '')::text AS holder_name,
  COALESCE(p.kind, CASE WHEN pg.id IS NOT NULL THEN 'group' END, '')::text AS holder_kind
FROM grants g
LEFT JOIN principals p ON g.subject_kind = 'principal' AND p.id = g.subject_id
LEFT JOIN principal_groups pg ON g.subject_kind = 'group' AND pg.id = g.subject_id
WHERE g.target = @target AND g.revoked_at_ms IS NULL
  AND (@subject::text = '' OR g.subject = @subject::text)
  AND (@role::text = '' OR g.role = @role::text)
  AND (g.subject > @after_subject OR (g.subject = @after_subject AND g.role > @after_role))
ORDER BY g.subject, g.role
LIMIT @row_limit;

-- name: CountGrantsOn :one
SELECT COUNT(*) FROM grants
WHERE target = @target AND revoked_at_ms IS NULL
  AND (@subject::text = '' OR subject = @subject::text)
  AND (@role::text = '' OR role = @role::text);

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
SELECT COUNT(*) FROM grants
WHERE role = $1 AND object_kind IN ('namespace', 'collection', 'unit') AND revoked_at_ms IS NULL;

-- name: CountRoleGrantsOnPrincipal :one
SELECT COUNT(*) FROM grants WHERE role = $1 AND object_kind = 'principal' AND revoked_at_ms IS NULL;

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
LEFT JOIN group_members m ON m.principal_id = p.id AND m.removed_at_ms IS NULL AND m.state = 'active'
WHERE p.subject COLLATE "C" = @subject::text AND p.deleted_at_ms IS NULL;

-- name: SubjectsOfEnabledCaller :many
-- The subjects of a caller as the check of a grant write reads them: a
-- principal that is disabled has none.
SELECT p.id AS principal_id, COALESCE(m.group_id, '')::text AS group_id
FROM principals p
LEFT JOIN group_members m ON m.principal_id = p.id AND m.removed_at_ms IS NULL AND m.state = 'active'
WHERE p.subject COLLATE "C" = @subject::text AND p.deleted_at_ms IS NULL AND p.disabled_at_ms IS NULL;

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
     AND COALESCE(c.removed_at_ms, 9223372036854775807) > @as_of_ms::bigint
     AND c.state = 'active') AS member_count
FROM grants g
LEFT JOIN principals p ON g.subject_kind = 'principal' AND p.id = g.subject_id
LEFT JOIN principal_groups pg ON g.subject_kind = 'group' AND pg.id = g.subject_id
LEFT JOIN group_members m ON g.subject_kind = 'group' AND m.group_id = g.subject_id
  AND ((@principal::text <> '' AND m.principal_id = @principal::text)
    OR (@principal::text = '' AND @expand::boolean))
  AND m.state = 'active'
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
        OR (@history::boolean AND EXISTS (SELECT 1 FROM roles r WHERE r.id = g.role))))
    OR (@token_namespace::text <> '' AND g.target = @token_namespace::text
      AND (g.role = ANY(@token_namespace_roles::text[])
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
  AND m.state = 'active'
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
        OR (@history::boolean AND EXISTS (SELECT 1 FROM roles r WHERE r.id = g.role))))
    OR (@token_namespace::text <> '' AND g.target = @token_namespace::text
      AND (g.role = ANY(@token_namespace_roles::text[])
        OR (@history::boolean AND EXISTS (SELECT 1 FROM roles r WHERE r.id = g.role)))))
  AND (@subject::text = '' OR g.subject = @subject::text
    OR (@principal::text <> '' AND m.principal_id IS NOT NULL))
  AND CASE WHEN @history::boolean THEN
    g.created_at_ms < @to_ms::bigint AND COALESCE(g.revoked_at_ms, 9223372036854775807) > @from_ms::bigint
  ELSE g.created_at_ms <= @as_of_ms::bigint AND COALESCE(g.revoked_at_ms, 9223372036854775807) > @as_of_ms::bigint END;

-- name: CallerGrants :many
-- One page of the current grants of a caller (ADR 52): the grants of the
-- principal, and the grants of each of its groups through its membership
-- at as_of_ms. Each holder reads its grants from grants_holder, so the cost
-- follows the grants of the caller, not the grants of the installation. The
-- IDs order with the C collation, as in SQLite.
WITH holders (kind, id) AS (
  SELECT 'principal'::text, @principal::text
  UNION ALL SELECT 'group'::text, unnest(@groups::text[])
)
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
     AND COALESCE(c.removed_at_ms, 9223372036854775807) > @as_of_ms::bigint
     AND c.state = 'active') AS member_count
FROM holders h
JOIN grants g ON g.subject_kind = h.kind AND g.subject_id = h.id AND g.revoked_at_ms IS NULL
LEFT JOIN principals p ON g.subject_kind = 'principal' AND p.id = g.subject_id
LEFT JOIN principal_groups pg ON g.subject_kind = 'group' AND pg.id = g.subject_id
LEFT JOIN group_members m ON g.subject_kind = 'group' AND m.group_id = g.subject_id
  AND m.principal_id = @principal::text
  AND m.state = 'active'
  AND m.added_at_ms <= @as_of_ms::bigint AND COALESCE(m.removed_at_ms, 9223372036854775807) > @as_of_ms::bigint
LEFT JOIN principals mp ON mp.id = m.principal_id
WHERE (g.subject_kind = 'principal' OR m.principal_id IS NOT NULL)
  AND g.created_at_ms <= @as_of_ms::bigint
  AND (g.created_at_ms > @after_at_ms::bigint
    OR (g.created_at_ms = @after_at_ms::bigint
      AND (g.id COLLATE "C" > @after_id::text
        OR (g.id = @after_id::text
          AND (COALESCE(m.added_at_ms, 0) > @after_member_at_ms::bigint
            OR (COALESCE(m.added_at_ms, 0) = @after_member_at_ms::bigint
              AND COALESCE(m.principal_id, '') COLLATE "C" > @after_member_id::text))))))
ORDER BY g.created_at_ms, g.id COLLATE "C", member_added_at_ms, COALESCE(m.principal_id, '') COLLATE "C"
LIMIT @row_limit::bigint;

-- name: CountCallerGrants :one
WITH holders (kind, id) AS (
  SELECT 'principal'::text, @principal::text
  UNION ALL SELECT 'group'::text, unnest(@groups::text[])
)
SELECT COUNT(*)
FROM holders h
JOIN grants g ON g.subject_kind = h.kind AND g.subject_id = h.id AND g.revoked_at_ms IS NULL
LEFT JOIN group_members m ON g.subject_kind = 'group' AND m.group_id = g.subject_id
  AND m.principal_id = @principal::text
  AND m.state = 'active'
  AND m.added_at_ms <= @as_of_ms::bigint AND COALESCE(m.removed_at_ms, 9223372036854775807) > @as_of_ms::bigint
WHERE (g.subject_kind = 'principal' OR m.principal_id IS NOT NULL)
  AND g.created_at_ms <= @as_of_ms::bigint;

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
-- The token, its owner and the token policy of the installation in one read,
-- so a policy change applies to the next lookup on every instance.
SELECT t.id, t.principal_id, t.name, t.kind, t.digest, t.created_at_ms, t.expires_at_ms, t.last_used_at_ms,
  t.revoked_at_ms, t.replaced_by, t.created_by, t.disabled_at_ms, p.subject, p.display_name, p.kind AS principal_kind,
  p.disabled_at_ms AS principal_disabled_at_ms, p.break_glass AS principal_break_glass,
  GREATEST(p.created_at_ms, p.last_active_at_ms, COALESCE(p.last_sign_in_at_ms, 0), COALESCE((
    SELECT MAX(GREATEST(o.created_at_ms, COALESCE(o.last_used_at_ms, 0))) FROM access_tokens o WHERE o.principal_id = p.id
  ), 0), COALESCE((
    SELECT MAX(s.last_seen_at_ms) FROM sessions s WHERE s.principal_id = p.id
  ), 0))::bigint AS principal_active_at_ms,
  ap.max_lifetime_ms AS policy_max_lifetime_ms, ap.inactivity_ms AS policy_inactivity_ms,
  ap.disable_tokens AS policy_disable_tokens, ap.disable_accounts AS policy_disable_accounts,
  ap.disable_break_glass AS policy_disable_break_glass, ap.updated_at_ms AS policy_updated_at_ms
FROM access_tokens t
JOIN principals p ON p.id = t.principal_id
CROSS JOIN access_token_policy ap
WHERE t.digest = $1 AND p.deleted_at_ms IS NULL AND ap.id = 1;

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
-- name: CreateCollection :execrows
-- The caller checks the fence in the same transaction. A row that exists
-- under the same key (the name in lower case) is not written.
INSERT INTO collections (id, namespace, name_key, name, created_at_ms)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (namespace, name_key) DO NOTHING;

-- name: GetCollection :one
SELECT id, namespace, name, created_at_ms
FROM collections
WHERE namespace = $1 AND name_key = $2;

-- name: ListCollections :many
SELECT id, namespace, name, created_at_ms
FROM collections
WHERE namespace = $1
ORDER BY name_key;

-- name: CountLiveCollectionMembers :one
-- Counts the units of a collection that the inventory still holds. A unit that
-- was deleted has left the inventory, and its membership does not count.
SELECT COUNT(*)
FROM collection_members AS m
JOIN inventory AS i ON i.unit_id = m.unit_id
WHERE m.namespace = $1 AND m.collection_key = $2;

-- name: DeleteCollectionMembers :exec
-- Removes the memberships of a collection. The caller removes them only after
-- it found no live unit, so the rows that remain belong to deleted units.
DELETE FROM collection_members
WHERE namespace = $1 AND collection_key = $2;

-- name: DeleteCollection :execrows
DELETE FROM collections
WHERE namespace = $1 AND name_key = $2;

-- name: GetInventoryUnit :one
-- The inventory row of one unit: its ID. The collection is read by a join on
-- the unit ID, not from the inventory.
SELECT unit_id
FROM inventory
WHERE namespace = $1 AND tool = $2 AND name = $3;

-- name: ContextUnitExists :one
-- Whether the namespace holds the unit in its live inventory or in its deleted
-- units. A context write checks it in its own transaction, so a purge that
-- committed before the write refuses the write.
SELECT (EXISTS (SELECT 1 FROM inventory AS i WHERE i.namespace = $1 AND i.tool = $2 AND i.unit_id = $3)
    OR EXISTS (SELECT 1 FROM inventory_deleted AS d WHERE d.namespace = $1 AND d.tool = $2 AND d.unit_id = $3))::boolean AS present;

-- name: GetUnitCollection :one
-- The collection of one unit by its ID, as written.
SELECT c.name
FROM collection_members AS m
JOIN collections AS c ON c.namespace = m.namespace AND c.name_key = m.collection_key
WHERE m.unit_id = $1;

-- name: UnitCollectionID :one
-- The stable ID of the collection of one unit by its ID.
SELECT c.id
FROM collection_members AS m
JOIN collections AS c ON c.namespace = m.namespace AND c.name_key = m.collection_key
WHERE m.unit_id = $1;

-- name: UpsertUnitCollection :exec
INSERT INTO collection_members (unit_id, namespace, collection_key, assigned_at_ms)
VALUES ($1, $2, $3, $4)
ON CONFLICT (unit_id) DO UPDATE SET
    namespace = excluded.namespace,
    collection_key = excluded.collection_key,
    assigned_at_ms = excluded.assigned_at_ms;


-- name: LastAccessByPrincipals :many
-- The latest use of each given principal: the last use of its access
-- tokens and of its console sessions, and its last sign-in. One row for
-- each principal with a used token, a session or a sign-in. Revoked, replaced and expired tokens and ended
-- sessions count: a use is a use.
SELECT u.principal_id, MAX(u.used_at_ms)::bigint AS last_used_at_ms
FROM (
  SELECT t.principal_id, t.last_used_at_ms AS used_at_ms
  FROM access_tokens t
  WHERE t.principal_id = ANY(sqlc.arg('principal_ids')::text[]) AND t.last_used_at_ms IS NOT NULL
  UNION ALL
  SELECT s.principal_id, s.last_seen_at_ms AS used_at_ms
  FROM sessions s
  WHERE s.principal_id = ANY(sqlc.arg('principal_ids')::text[])
  UNION ALL
  SELECT p.id AS principal_id, p.last_sign_in_at_ms AS used_at_ms
  FROM principals p
  WHERE p.id = ANY(sqlc.arg('principal_ids')::text[]) AND p.last_sign_in_at_ms IS NOT NULL
) u
GROUP BY u.principal_id;

-- name: GetAuditChainState :one
-- The audit chain state that this installation keeps: the state, when it
-- last changed, who changed it, whether a change waits for an approval,
-- whether the chain entered maintenance mode itself, whether a restore
-- replaced the log, and when the running migration started, 0 when none
-- runs. No row means the chain is on.
SELECT state, changed_at_ms, changed_by, pending, entered_maintenance, log_replaced, migrating_at_ms FROM audit_chain_state WHERE id = 1;

-- name: SetAuditChainState :exec
-- Stores the audit chain state of this installation.
INSERT INTO audit_chain_state (id, state, changed_at_ms, changed_by, pending, entered_maintenance, log_replaced, migrating_at_ms)
VALUES (1, $1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO UPDATE SET state = EXCLUDED.state, changed_at_ms = EXCLUDED.changed_at_ms,
    changed_by = EXCLUDED.changed_by, pending = EXCLUDED.pending,
    entered_maintenance = EXCLUDED.entered_maintenance, log_replaced = EXCLUDED.log_replaced,
    migrating_at_ms = EXCLUDED.migrating_at_ms;

-- name: LastAuditChainStartSequence :one
-- The sequence of the last start entry of the audit chain of a namespace,
-- or 0 when there is none. The chain check starts there.
SELECT CAST(COALESCE(MAX(sequence), 0) AS BIGINT) FROM audit_entries
WHERE namespace = $1 AND action = 'server:start-audit-chain' AND sequence >= 1;

-- name: MinAuditSequence :one
-- The lowest sequence of a namespace, or 1 when it has no entry. The chain
-- check fails a namespace with a row below 1, because the walk starts at 1.
SELECT CAST(COALESCE(MIN(sequence), 1) AS BIGINT) FROM audit_entries WHERE namespace = $1;

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

-- name: ListBlobPurgesAfter :many
-- The page of ListBlobPurges after the cursor (purge time, tool, unit ID).
-- The lower bound on purged_at_ms lets the index scan start at the cursor.
SELECT tool, unit_id, namespace, purged_at_ms
FROM blob_purges
WHERE purged_at_ms >= sqlc.arg(after_ms)
  AND (purged_at_ms > sqlc.arg(after_ms)
    OR (purged_at_ms = sqlc.arg(after_ms) AND tool COLLATE "C" > sqlc.arg(after_tool))
    OR (purged_at_ms = sqlc.arg(after_ms) AND tool = sqlc.arg(after_tool)
      AND unit_id COLLATE "C" > sqlc.arg(after_unit_id)))
ORDER BY purged_at_ms, tool COLLATE "C", unit_id COLLATE "C"
LIMIT sqlc.arg(page_limit);

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

-- name: ClaimSinkOperation :one
-- Claims the sink operation id for a new holder: the state returns to
-- running, the attempt and the claim token grow, and the expiry is the
-- database clock plus the TTL. The caller holds the row, checked the claim
-- first, and passes the new token. It adds add_expiries to the expiries:
-- one when the claim takes over an expired claim, none for a first claim.
UPDATE durable_operations
SET state = 'running', attempt = attempt + 1, expiries = expiries + sqlc.arg('add_expiries')::bigint,
  claim_owner = sqlc.arg('owner')::text, claim_token = sqlc.arg('token')::bigint,
  claim_expires_at_ms = sqlc.arg('expires_at_ms')::bigint, updated_at_ms = sqlc.arg('now_ms')::bigint
WHERE id = sqlc.arg('id')::text
RETURNING claim_token, claim_expires_at_ms;

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
-- A token of a break-glass account is always exempt from the inactivity
-- disable.
UPDATE access_tokens SET disabled_at_ms = sqlc.arg('at_ms')::bigint
WHERE revoked_at_ms IS NULL AND disabled_at_ms IS NULL AND expires_at_ms > sqlc.arg('at_ms')::bigint
  AND COALESCE(last_used_at_ms, created_at_ms) <= sqlc.arg('cutoff_ms')::bigint
  AND NOT EXISTS (
    SELECT 1 FROM principals p
    WHERE p.id = access_tokens.principal_id AND p.break_glass = TRUE
  )
RETURNING id, principal_id;

-- name: DisableInactivePrincipals :many
-- A person is inactive when its creation, its activity baseline, its last
-- sign-in, the creation and the last use of each of its tokens, and the
-- last use of each of its console sessions are all at or before the
-- cutoff. The session use counts by itself, so the result does not depend
-- on the maximum length of a session. A break-glass account is always
-- exempt from the inactivity disable.
UPDATE principals SET disabled_at_ms = sqlc.arg('at_ms')::bigint
WHERE kind = 'person' AND deleted_at_ms IS NULL AND disabled_at_ms IS NULL
  AND break_glass = FALSE
  AND created_at_ms <= sqlc.arg('cutoff_ms')::bigint
  AND last_active_at_ms <= sqlc.arg('cutoff_ms')::bigint
  AND COALESCE(last_sign_in_at_ms, 0) <= sqlc.arg('cutoff_ms')::bigint
  AND NOT EXISTS (
    SELECT 1 FROM access_tokens t
    WHERE t.principal_id = principals.id
      AND (t.created_at_ms > sqlc.arg('cutoff_ms')::bigint OR t.last_used_at_ms > sqlc.arg('cutoff_ms')::bigint)
  )
  AND NOT EXISTS (
    SELECT 1 FROM sessions s
    WHERE s.principal_id = principals.id AND s.last_seen_at_ms > sqlc.arg('cutoff_ms')::bigint
  )
RETURNING id;

-- name: EnablePrincipal :execrows
-- An enable also sets the activity baseline from the database clock, the
-- clock of the sweep, so the next sweep does not disable the principal again.
UPDATE principals SET disabled_at_ms = NULL, updated_at_ms = sqlc.arg('updated_at_ms')::bigint,
  last_active_at_ms = FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint
WHERE id = sqlc.arg('id') AND deleted_at_ms IS NULL AND disabled_at_ms IS NOT NULL;

-- name: DeleteCollectionMembersOfUnit :exec
-- Removes the membership of one unit. The purge of a unit calls it in the same
-- transaction as the purge of its rows (ADR 47, ADR 51).
DELETE FROM collection_members
WHERE unit_id = $1;

-- name: DeleteMissingCollectionMembership :exec
-- Removes the membership of one unit when its collection no longer exists. A
-- restore calls it, so a restored unit never points at a missing collection.
DELETE FROM collection_members
WHERE unit_id = $1 AND NOT EXISTS (
    SELECT 1
    FROM collections AS c
    WHERE c.namespace = collection_members.namespace AND c.name_key = collection_members.collection_key
);

-- name: GrantIDsOnTarget :many
-- The IDs of the current grants on one target, in order. A deleted collection
-- revokes each of them, so that none outlives it.
SELECT id FROM grants
WHERE target = $1 AND revoked_at_ms IS NULL
ORDER BY id;

-- name: CreateSession :execrows
-- A session needs a person principal that exists and is not disabled. A
-- taken ID or digest inserts nothing.
INSERT INTO sessions (id, principal_id, digest, provider, created_at_ms, last_seen_at_ms, auth_time_ms, source_ip, user_agent)
SELECT sqlc.arg('id'), p.id, sqlc.arg('digest'), sqlc.arg('provider'), sqlc.arg('created_at_ms')::bigint,
  sqlc.arg('created_at_ms')::bigint, sqlc.arg('auth_time_ms')::bigint, sqlc.arg('source_ip'), sqlc.arg('user_agent')
FROM principals p
WHERE p.id = sqlc.arg('principal_id') AND p.deleted_at_ms IS NULL AND p.disabled_at_ms IS NULL AND p.kind = 'person'
ON CONFLICT DO NOTHING;

-- name: GetSessionPrincipal :one
-- The kind and the disable time of a principal that exists, so a refused
-- session insert can name its cause.
SELECT kind, disabled_at_ms FROM principals WHERE id = $1 AND deleted_at_ms IS NULL;

-- name: SetLastSignIn :exec
UPDATE principals SET last_sign_in_at_ms = GREATEST(COALESCE(last_sign_in_at_ms, 0), sqlc.arg('at_ms')::bigint)
WHERE id = sqlc.arg('id');

-- name: GetSession :one
SELECT id, principal_id, provider, created_at_ms, last_seen_at_ms, auth_time_ms, ended_at_ms, end_reason, source_ip, user_agent
FROM sessions WHERE id = $1;

-- name: GetSessionByDigest :one
-- principal_active_at_ms is the latest activity of the owner by the account
-- rule of the inactivity disable, as in GetAccessTokenByDigest. The session
-- policy and the account part of the token policy come from the same read,
-- so a policy change applies to the next lookup on every instance.
SELECT s.id, s.principal_id, s.digest, s.provider, s.created_at_ms, s.last_seen_at_ms, s.auth_time_ms, s.ended_at_ms,
  s.end_reason, s.source_ip, s.user_agent, p.subject, p.display_name, p.kind AS principal_kind,
  p.disabled_at_ms AS principal_disabled_at_ms, p.break_glass AS principal_break_glass,
  GREATEST(p.created_at_ms, p.last_active_at_ms, COALESCE(p.last_sign_in_at_ms, 0), COALESCE((
    SELECT MAX(GREATEST(o.created_at_ms, COALESCE(o.last_used_at_ms, 0))) FROM access_tokens o WHERE o.principal_id = p.id
  ), 0), COALESCE((
    SELECT MAX(x.last_seen_at_ms) FROM sessions x WHERE x.principal_id = p.id
  ), 0))::bigint AS principal_active_at_ms,
  sp.idle_ms AS policy_idle_ms, sp.max_length_ms AS policy_max_length_ms, sp.updated_at_ms AS policy_updated_at_ms,
  sp.token_freshness_ms AS policy_token_freshness_ms,
  ap.inactivity_ms AS accounts_inactivity_ms, ap.disable_accounts AS accounts_disable
FROM sessions s
JOIN principals p ON p.id = s.principal_id
CROSS JOIN session_policy sp
CROSS JOIN access_token_policy ap
WHERE s.digest = $1 AND p.deleted_at_ms IS NULL AND sp.id = 1 AND ap.id = 1;

-- name: TouchSession :exec
-- An idle or expired session keeps its last use: seen_after_ms and
-- created_after_ms are the bounds of an active session at at_ms.
UPDATE sessions SET last_seen_at_ms = sqlc.arg('at_ms')::bigint
WHERE id = sqlc.arg('id') AND ended_at_ms IS NULL AND last_seen_at_ms <= sqlc.arg('before_ms')::bigint
  AND last_seen_at_ms > sqlc.arg('seen_after_ms')::bigint AND created_at_ms > sqlc.arg('created_after_ms')::bigint;

-- name: EndSession :execrows
UPDATE sessions SET ended_at_ms = sqlc.arg('at_ms')::bigint, end_reason = sqlc.arg('end_reason')
WHERE id = sqlc.arg('id') AND ended_at_ms IS NULL;

-- name: CountSession :one
SELECT COUNT(*) FROM sessions WHERE id = $1;

-- name: CreateSignInRequest :execrows
INSERT INTO sign_in_requests (digest, provider, nonce, verifier, return_path, created_at_ms, expires_at_ms, max_age_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT DO NOTHING;

-- name: TakeSignInRequest :one
DELETE FROM sign_in_requests WHERE digest = $1
RETURNING provider, nonce, verifier, return_path, created_at_ms, expires_at_ms, max_age_ms;

-- name: PruneSignInRequests :execrows
DELETE FROM sign_in_requests WHERE expires_at_ms <= $1;

-- name: GetSessionPolicy :one
SELECT idle_ms, max_length_ms, updated_at_ms, token_freshness_ms FROM session_policy WHERE id = 1;

-- name: SetSessionPolicy :execrows
UPDATE session_policy
SET idle_ms = sqlc.arg('idle_ms')::bigint, max_length_ms = sqlc.arg('max_length_ms')::bigint,
  updated_at_ms = sqlc.arg('updated_at_ms')::bigint, token_freshness_ms = sqlc.narg('token_freshness_ms')::bigint
WHERE id = 1;

-- name: InsertOIDCProvider :exec
-- The first start with a provider ID records its issuer. A later start
-- keeps the record.
INSERT INTO oidc_providers (provider_id, issuer, created_at_ms) VALUES ($1, $2, $3)
ON CONFLICT (provider_id) DO NOTHING;

-- name: GetOIDCProviderIssuer :one
SELECT issuer FROM oidc_providers WHERE provider_id = $1;

-- name: CreateInstallationSecretKey :execrows
-- The first seal in an installation scope stores its key. A concurrent
-- first seal keeps the stored key.
INSERT INTO installation_secret_keys (scope_id, wrapper_id, ciphertext, created_at_ms)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING;

-- name: GetInstallationSecretKey :one
SELECT scope_id, wrapper_id, ciphertext, created_at_ms
FROM installation_secret_keys
WHERE scope_id = $1;

-- name: ListInstallationWrapperIDs :many
SELECT DISTINCT wrapper_id
FROM installation_secret_keys
ORDER BY wrapper_id;

-- name: CreateSignInProvider :execrows
INSERT INTO sign_in_providers (
    provider_id, issuer, display_name, client_id, client_secret, scopes, auto_create, enabled,
    created_at_ms, updated_at_ms, version, mode, authorization_url, token_url, userinfo_url, subject_claim,
    name_claim, username_claim, email_claim
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
ON CONFLICT DO NOTHING;

-- name: GetSignInProvider :one
SELECT provider_id, issuer, display_name, client_id, client_secret, scopes, auto_create, enabled,
    created_at_ms, updated_at_ms, version, mode, authorization_url, token_url, userinfo_url, subject_claim,
    name_claim, username_claim, email_claim
FROM sign_in_providers
WHERE provider_id = $1;

-- name: ListSignInProviders :many
SELECT provider_id, issuer, display_name, client_id, client_secret, scopes, auto_create, enabled,
    created_at_ms, updated_at_ms, version, mode, authorization_url, token_url, userinfo_url, subject_claim,
    name_claim, username_claim, email_claim
FROM sign_in_providers
ORDER BY provider_id COLLATE "C";

-- name: UpdateSignInProvider :execrows
-- The update applies only to the version that the change read. The mode
-- never changes.
UPDATE sign_in_providers
SET issuer = sqlc.arg('issuer'), display_name = sqlc.arg('display_name'), client_id = sqlc.arg('client_id'),
    client_secret = sqlc.arg('client_secret'), scopes = sqlc.arg('scopes'),
    auto_create = sqlc.arg('auto_create'), enabled = sqlc.arg('enabled'),
    authorization_url = sqlc.arg('authorization_url'), token_url = sqlc.arg('token_url'),
    userinfo_url = sqlc.arg('userinfo_url'), subject_claim = sqlc.arg('subject_claim'),
    name_claim = sqlc.arg('name_claim'), username_claim = sqlc.arg('username_claim'),
    email_claim = sqlc.arg('email_claim'),
    updated_at_ms = sqlc.arg('updated_at_ms'), version = sqlc.arg('version')
WHERE provider_id = sqlc.arg('provider_id') AND version = sqlc.arg('version') - 1;

-- name: CountSignInProvider :one
SELECT COUNT(*) FROM sign_in_providers WHERE provider_id = $1;

-- name: GetSignInProviderEnabled :one
-- Reads whether a stored provider is enabled, with a shared lock, in the
-- transaction that inserts a session of it. A disable waits for that
-- transaction, and its session end then sees the new session.
SELECT enabled FROM sign_in_providers WHERE provider_id = $1 FOR SHARE;

-- name: DeleteSignInProvider :execrows
DELETE FROM sign_in_providers WHERE provider_id = $1;

-- name: EndSessionsWithSubjectPrefix :execrows
-- Ends every active session of a principal whose subject starts with the
-- prefix "<provider ID>:". The compare is exact, as in
-- CountPrincipalsWithSubjectPrefix.
UPDATE sessions SET ended_at_ms = sqlc.arg('at_ms')::bigint, end_reason = sqlc.arg('end_reason')
WHERE ended_at_ms IS NULL
  AND principal_id IN (SELECT id FROM principals WHERE starts_with(subject, sqlc.arg('prefix')));

-- name: CountPrincipalsWithSubjectPrefix :one
-- The prefix is "<provider ID>:". The compare is exact, with no pattern
-- and no case folding. Deleted principals count too.
SELECT COUNT(*) FROM principals WHERE starts_with(subject, sqlc.arg('prefix'));

-- Durable operations. Every time comes from the database clock, read with
-- clock_timestamp() when the statement runs, so neither the clock of an
-- instance nor the start of a long transaction moves a decision.

-- name: InsertDurableOperation :one
-- Creates a pending operation that can run at once. It inserts nothing when
-- the start key names an operation, and then returns no row.
INSERT INTO durable_operations (
  id, start_key, kind, input, version, state, attempt, expiries, failures, last_error, claim_owner, claim_token, claim_expires_at_ms,
  cancel_requested, not_before_ms, created_at_ms, updated_at_ms
)
SELECT sqlc.arg('id')::text, sqlc.arg('start_key')::text, sqlc.arg('kind')::text, sqlc.arg('input')::bytea,
  sqlc.arg('version')::bigint, 'pending', 0, 0, 0, '', NULL, 0, NULL, FALSE, c.now_ms, c.now_ms, c.now_ms
FROM (SELECT FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint AS now_ms) c
ON CONFLICT (start_key) DO NOTHING
RETURNING id, start_key, kind, input, version, state, attempt, expiries, failures, last_error, claim_owner, claim_token, claim_expires_at_ms,
  cancel_requested, not_before_ms, created_at_ms, updated_at_ms;

-- name: GetDurableOperation :one
SELECT id, start_key, kind, input, version, state, attempt, expiries, failures, last_error, claim_owner, claim_token, claim_expires_at_ms,
  cancel_requested, not_before_ms, created_at_ms, updated_at_ms
FROM durable_operations WHERE id = $1;

-- name: GetDurableOperationByKey :one
SELECT id, start_key, kind, input, version, state, attempt, expiries, failures, last_error, claim_owner, claim_token, claim_expires_at_ms,
  cancel_requested, not_before_ms, created_at_ms, updated_at_ms
FROM durable_operations WHERE start_key = $1;

-- name: CountRunnableDurableOperations :one
-- The count of operations of one kind that can run now: pending or running.
-- An approval of an audit chain change reads it in the transaction of its
-- own start, while it holds the advisory lock of the server audit scope, so
-- a second approval waits for the first and refuses.
SELECT COUNT(*) FROM durable_operations WHERE kind = $1 AND state IN ('pending', 'running');

-- name: LockDurableOperation :one
-- Holds the row of one operation until the transaction ends, so the checks
-- and the write of a holder see one state.
SELECT id, start_key, kind, input, version, state, attempt, expiries, failures, last_error, claim_owner, claim_token, claim_expires_at_ms,
  cancel_requested, not_before_ms, created_at_ms, updated_at_ms
FROM durable_operations WHERE id = $1
FOR UPDATE;

-- name: ListDurableOperations :many
-- An empty kind or state list matches every operation. A limit of 0 means
-- no limit.
SELECT id, start_key, kind, input, version, state, attempt, expiries, failures, last_error, claim_owner, claim_token, claim_expires_at_ms,
  cancel_requested, not_before_ms, created_at_ms, updated_at_ms
FROM durable_operations
WHERE (sqlc.arg('kind')::text = '' OR kind = sqlc.arg('kind')::text)
  AND (cardinality(sqlc.arg('states')::text[]) = 0 OR state = ANY(sqlc.arg('states')::text[]))
ORDER BY created_at_ms, id
LIMIT NULLIF(sqlc.arg('max_rows')::bigint, 0);

-- name: ListDurableSteps :many
SELECT operation_id, name, seq, result, done_at_ms
FROM durable_steps
WHERE operation_id = ANY(sqlc.arg('ids')::text[])
ORDER BY operation_id, seq;

-- name: EndExpiredCanceledDurableOperations :exec
-- Ends canceled each running operation of the kinds whose claim expired
-- after a cancel request. kinds and max_versions are pairs: a claim takes an
-- operation of a kind only up to its version limit. A row that another claim
-- holds is skipped.
WITH c AS MATERIALIZED (
  SELECT FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint AS now_ms
), ended AS (
  SELECT o.id FROM durable_operations o, c
  WHERE o.kind = ANY(sqlc.arg('kinds')::text[])
    AND o.version <= (sqlc.arg('max_versions')::bigint[])[array_position(sqlc.arg('kinds')::text[], o.kind)] AND o.state = 'running' AND o.cancel_requested
    AND o.claim_expires_at_ms <= c.now_ms
  FOR UPDATE OF o SKIP LOCKED
)
UPDATE durable_operations o
SET state = 'canceled', claim_owner = NULL, claim_expires_at_ms = NULL, updated_at_ms = c.now_ms
FROM c, ended
WHERE o.id = ended.id;

-- name: ClaimDurableOperation :one
-- Claims the oldest operation of the kinds, up to the version limit of its
-- kind, that can run now: a pending one
-- whose not_before_ms passed, or a running one whose claim expired without a
-- cancel request. A row that another claim holds is skipped, so of several
-- concurrent claims of one operation at most one gets it. The expiry is the
-- database clock plus the lease. A claim that takes over an expired claim
-- counts one more expiry.
WITH c AS MATERIALIZED (
  SELECT FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint AS now_ms
), pick AS (
  SELECT o.id FROM durable_operations o, c
  WHERE o.kind = ANY(sqlc.arg('kinds')::text[])
    AND o.version <= (sqlc.arg('max_versions')::bigint[])[array_position(sqlc.arg('kinds')::text[], o.kind)] AND (
    (o.state = 'pending' AND o.not_before_ms <= c.now_ms)
    OR (o.state = 'running' AND o.claim_expires_at_ms <= c.now_ms AND NOT o.cancel_requested)
  )
  ORDER BY o.created_at_ms, o.id
  LIMIT 1
  FOR UPDATE OF o SKIP LOCKED
)
UPDATE durable_operations o
SET state = 'running', attempt = o.attempt + 1, claim_token = o.claim_token + 1,
  expiries = o.expiries + CASE WHEN o.state = 'running' THEN 1 ELSE 0 END,
  claim_owner = sqlc.arg('owner')::text, claim_expires_at_ms = c.now_ms + sqlc.arg('lease_ms')::bigint,
  updated_at_ms = c.now_ms
FROM c, pick
WHERE o.id = pick.id
RETURNING o.id, o.start_key, o.kind, o.input, o.version, o.state, o.attempt, o.expiries, o.failures, o.last_error, o.claim_owner, o.claim_token,
  o.claim_expires_at_ms, o.cancel_requested, o.not_before_ms, o.created_at_ms, o.updated_at_ms;

-- name: ExtendDurableClaim :exec
UPDATE durable_operations
SET claim_expires_at_ms = sqlc.arg('expires_at_ms')::bigint, updated_at_ms = sqlc.arg('now_ms')::bigint
WHERE id = sqlc.arg('id')::text;

-- name: DurableStepExists :one
SELECT EXISTS (SELECT 1 FROM durable_steps WHERE operation_id = $1 AND name = $2) AS done;

-- name: InsertDurableStep :exec
-- The caller holds the row of the operation, so the next seq is safe.
INSERT INTO durable_steps (operation_id, name, seq, result, done_at_ms)
SELECT sqlc.arg('operation_id')::text, sqlc.arg('name')::text, COALESCE(MAX(seq), 0) + 1,
  sqlc.arg('result')::bytea, sqlc.arg('now_ms')::bigint
FROM durable_steps WHERE operation_id = sqlc.arg('operation_id')::text;

-- name: TouchDurableOperation :exec
UPDATE durable_operations SET updated_at_ms = sqlc.arg('now_ms')::bigint WHERE id = sqlc.arg('id')::text;

-- name: EndDurableClaim :exec
-- Ends the claim of an operation with its next state, and adds add_failures
-- to its failures. The claim token stays, so the next claim gets a larger
-- one.
UPDATE durable_operations
SET state = sqlc.arg('state')::text, last_error = sqlc.arg('last_error')::text,
  failures = failures + sqlc.arg('add_failures')::bigint,
  not_before_ms = sqlc.arg('not_before_ms')::bigint, claim_owner = NULL, claim_expires_at_ms = NULL,
  updated_at_ms = sqlc.arg('now_ms')::bigint
WHERE id = sqlc.arg('id')::text;

-- name: CancelDurableOperation :exec
UPDATE durable_operations
SET state = sqlc.arg('state')::text, cancel_requested = sqlc.arg('cancel_requested')::boolean,
  updated_at_ms = sqlc.arg('now_ms')::bigint
WHERE id = sqlc.arg('id')::text;

-- Sign-in failure budget. Every time comes from DatabaseNowMs, the clock of
-- the store, read in the same transaction as the write.

-- name: AddSignInFailure :one
-- Adds one miss of source in the current window of length_ms, in one atomic
-- write, and answers the count of that window after the add with the time of
-- the add and the end of the window. Every time comes from the clock of the
-- store, read once in the statement, so the clock of an instance never moves
-- the window. Concurrent adds each count once.
WITH now_ms AS (
    SELECT FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint AS now
)
INSERT INTO sign_in_failures (source, window_end_ms, count)
SELECT sqlc.arg('source')::text, (now / sqlc.arg('length_ms')::bigint + 1) * sqlc.arg('length_ms')::bigint, 1
FROM now_ms
ON CONFLICT (source, window_end_ms) DO UPDATE SET count = sign_in_failures.count + 1
RETURNING window_end_ms, count, (SELECT now FROM now_ms) AS now_ms;

-- name: PruneSignInFailures :execrows
-- Deletes every window that ended age_ms or longer ago on the clock of the
-- store, read in the statement.
DELETE FROM sign_in_failures
WHERE window_end_ms <= (
    FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint - sqlc.arg('age_ms')::bigint
);

-- name: PruneSignInFailuresAt :execrows
-- Deletes every window that ended at or before threshold_ms. The caller
-- passes the threshold instead of the clock of the store, so a test can pin
-- the prune at the boundary of a window.
DELETE FROM sign_in_failures
WHERE window_end_ms <= sqlc.arg('threshold_ms')::bigint;



-- Durable schedules. Every time comes from the database clock, read with
-- clock_timestamp() when the statement runs, so neither the clock of an
-- instance nor the start of a long transaction moves a decision.

-- name: GetDurableSchedule :one
SELECT name, interval_ms, next_run_at_ms, last_claimed_at_ms FROM durable_schedules WHERE name = $1;

-- name: InsertDurableSchedule :exec
INSERT INTO durable_schedules (name, interval_ms, next_run_at_ms, last_claimed_at_ms)
VALUES (sqlc.arg('name')::text, sqlc.arg('interval_ms')::bigint, sqlc.arg('now_ms')::bigint, NULL);

-- name: SetDurableScheduleInterval :exec
-- Sets the interval, and moves the next run to now plus the interval when
-- that is earlier.
UPDATE durable_schedules
SET interval_ms = sqlc.arg('interval_ms')::bigint,
  next_run_at_ms = LEAST(next_run_at_ms, sqlc.arg('now_ms')::bigint + sqlc.arg('interval_ms')::bigint)
WHERE name = sqlc.arg('name')::text;

-- name: ClaimDurableScheduleTick :execrows
-- The conditional update that claims a due tick: it changes the row only
-- when next_run_at_ms is the value that the claim read and is due at now.
UPDATE durable_schedules
SET next_run_at_ms = sqlc.arg('now_ms')::bigint + interval_ms,
  last_claimed_at_ms = sqlc.arg('now_ms')::bigint
WHERE name = sqlc.arg('name')::text AND next_run_at_ms = sqlc.arg('due_at_ms')::bigint
  AND next_run_at_ms <= sqlc.arg('now_ms')::bigint;


-- name: GetNamespaceID :one
-- The stable ID of the namespace that has the name now.
SELECT id
FROM namespaces
WHERE name = $1;

-- name: GetCollectionNameByID :one
SELECT name
FROM collections
WHERE namespace = $1 AND id = $2;

-- name: ListObjectContext :many
-- The entries of one object, both sources, API first. The caller checks the
-- fence of the namespace in the same transaction for a write.
SELECT tool, source, fields, warning, item_id, item_seq, updated_at_ms, updated_by
FROM object_context
WHERE namespace_id = $1 AND object_kind = $2 AND object_id = $3 AND resource_key = $4
ORDER BY source;

-- name: UpsertObjectContext :exec
-- Writes one entry. The deleted mark of a resource and its order stay as
-- they are on a conflict.
INSERT INTO object_context (
    namespace_id, object_kind, object_id, tool, resource_key, source, fields, warning,
    item_id, item_seq, resource_deleted_at_ms, marks_seq, updated_at_ms, updated_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULL, 0, $11, $12)
ON CONFLICT (namespace_id, object_kind, object_id, resource_key, source) DO UPDATE
SET tool = excluded.tool,
    fields = excluded.fields,
    warning = excluded.warning,
    item_id = excluded.item_id,
    item_seq = excluded.item_seq,
    updated_at_ms = excluded.updated_at_ms,
    updated_by = excluded.updated_by;

-- name: DeleteObjectContext :execrows
-- Deletes one entry. The owner rows of the entry go with it.
DELETE FROM object_context
WHERE namespace_id = $1 AND object_kind = $2 AND object_id = $3 AND resource_key = $4 AND source = $5;

-- name: DeleteObjectOwners :exec
DELETE FROM object_context_owners
WHERE namespace_id = $1 AND object_kind = $2 AND object_id = $3 AND resource_key = $4 AND source = $5;

-- name: InsertObjectOwner :exec
INSERT INTO object_context_owners (namespace_id, owner, object_kind, object_id, resource_key, source)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT DO NOTHING;

-- name: GetUnitOutputSeq :one
-- The order of the state item that the output entries of a unit come from.
SELECT item_seq
FROM object_context
WHERE namespace_id = $1 AND object_kind = 'unit' AND object_id = $2 AND resource_key = '' AND source = 'output';

-- name: DeleteUnitOutputContext :exec
-- Deletes the output entries of a unit and of its resources.
DELETE FROM object_context
WHERE namespace_id = $1 AND object_kind IN ('unit', 'resource') AND object_id = $2 AND source = 'output';

-- name: DeleteUnitContext :exec
-- Deletes the entries of a unit and of its resources, both sources.
DELETE FROM object_context
WHERE namespace_id = $1 AND object_kind IN ('unit', 'resource') AND object_id = $2;

-- name: PageObjectContextByOwner :many
-- One page of the entries that name an owner, read from the owner index and
-- joined by the primary key of the entries.
SELECT c.object_kind, c.object_id, c.tool, c.resource_key, c.source, c.fields, c.warning, c.item_id,
       c.item_seq, c.updated_at_ms, c.updated_by
FROM object_context_owners o
JOIN object_context c
    ON c.namespace_id = o.namespace_id AND c.object_kind = o.object_kind AND c.object_id = o.object_id
   AND c.resource_key = o.resource_key AND c.source = o.source
WHERE o.namespace_id = sqlc.arg(namespace_id) AND o.owner = sqlc.arg(owner)
  AND (o.object_kind, o.object_id, o.resource_key, o.source) > (sqlc.arg(after_kind), sqlc.arg(after_id), sqlc.arg(after_key), sqlc.arg(after_source))
ORDER BY o.object_kind, o.object_id, o.resource_key, o.source
LIMIT sqlc.arg(page_limit);

-- name: GetGateObject :one
-- One configuration object of a scope.
SELECT version, body
FROM gate_objects
WHERE namespace_id = sqlc.arg(namespace_id) AND scope_key = sqlc.arg(scope_key)
  AND kind = sqlc.arg(kind) AND id = sqlc.arg(id);

-- name: ListGateObjects :many
-- The objects of one kind in one scope, ordered by ID.
SELECT id, version, body
FROM gate_objects
WHERE namespace_id = sqlc.arg(namespace_id) AND scope_key = sqlc.arg(scope_key) AND kind = sqlc.arg(kind)
ORDER BY id COLLATE "C";

-- name: ListGateObjectsOfScope :many
-- Every object and setting of one scope, ordered by kind and ID.
SELECT kind, id, version, body
FROM gate_objects
WHERE namespace_id = sqlc.arg(namespace_id) AND scope_key = sqlc.arg(scope_key)
ORDER BY kind COLLATE "C", id COLLATE "C";

-- name: CountGateObjects :one
-- The count of objects of one kind in one scope.
SELECT COUNT(*)
FROM gate_objects
WHERE namespace_id = sqlc.arg(namespace_id) AND scope_key = sqlc.arg(scope_key) AND kind = sqlc.arg(kind);

-- name: InsertGateObject :exec
INSERT INTO gate_objects (namespace_id, scope_key, kind, id, level, tool, unit_name, version, body, updated_at_ms)
VALUES (sqlc.arg(namespace_id), sqlc.arg(scope_key), sqlc.arg(kind), sqlc.arg(id), sqlc.arg(level), sqlc.arg(tool),
        sqlc.arg(unit_name), sqlc.arg(version), sqlc.arg(body), sqlc.arg(updated_at_ms));

-- name: UpdateGateObject :execrows
-- Replaces an object when its version is the expected one.
UPDATE gate_objects
SET version = sqlc.arg(version), body = sqlc.arg(body), updated_at_ms = sqlc.arg(updated_at_ms)
WHERE namespace_id = sqlc.arg(namespace_id) AND scope_key = sqlc.arg(scope_key)
  AND kind = sqlc.arg(kind) AND id = sqlc.arg(id) AND version = sqlc.arg(prev_version);

-- name: DeleteGateObject :execrows
-- Removes an object when its version is the expected one. Its references go
-- with it.
DELETE FROM gate_objects
WHERE namespace_id = sqlc.arg(namespace_id) AND scope_key = sqlc.arg(scope_key)
  AND kind = sqlc.arg(kind) AND id = sqlc.arg(id) AND version = sqlc.arg(prev_version);

-- name: InsertGateReference :exec
INSERT INTO gate_references (ref_kind, ref_id, namespace_id, scope_key, owner_kind, owner_id)
VALUES (sqlc.arg(ref_kind), sqlc.arg(ref_id), sqlc.arg(namespace_id), sqlc.arg(scope_key),
        sqlc.arg(owner_kind), sqlc.arg(owner_id));

-- name: DeleteGateReferences :exec
DELETE FROM gate_references
WHERE namespace_id = sqlc.arg(namespace_id) AND scope_key = sqlc.arg(scope_key)
  AND owner_kind = sqlc.arg(owner_kind) AND owner_id = sqlc.arg(owner_id);

-- name: ListNarrowerGateObjects :many
-- The objects of one kind and ID in the scope and in the scopes after it: for
-- the installation, every scope; for a namespace, every scope of the
-- namespace; for a collection, the collection and the units that it holds
-- now; for a unit, the unit. level, namespace_id, scope_key and
-- collection_key describe the scope.
SELECT o.scope_key, o.version, o.body
FROM gate_objects AS o
WHERE o.kind = sqlc.arg(kind) AND o.id = sqlc.arg(id)
  AND (
      sqlc.arg(level)::text = 'installation'
      OR (sqlc.arg(level)::text = 'namespace' AND o.namespace_id = sqlc.arg(namespace_id))
      OR (o.namespace_id = sqlc.arg(namespace_id) AND o.scope_key = sqlc.arg(scope_key))
      OR (sqlc.arg(level)::text = 'collection' AND o.level = 'unit' AND o.namespace_id = sqlc.arg(namespace_id)
          AND EXISTS (
              SELECT 1
              FROM namespaces AS n
              JOIN inventory AS i ON i.namespace = n.name
              JOIN collection_members AS m ON m.unit_id = i.unit_id AND m.namespace = n.name
              WHERE n.id = o.namespace_id AND i.tool = o.tool AND i.name = o.unit_name
                AND m.collection_key = sqlc.arg(collection_key)))
  )
ORDER BY o.scope_key COLLATE "C";

-- name: ListGateReferrers :many
-- The objects, in the scope and in the scopes after it (see
-- ListNarrowerGateObjects), that name the approval rule or the path set.
SELECT o.scope_key, o.kind, o.id, o.version, o.body
FROM gate_references AS r
JOIN gate_objects AS o
    ON o.namespace_id = r.namespace_id AND o.scope_key = r.scope_key AND o.kind = r.owner_kind AND o.id = r.owner_id
WHERE r.ref_kind = sqlc.arg(ref_kind) AND r.ref_id = sqlc.arg(ref_id)
  AND (
      sqlc.arg(level)::text = 'installation'
      OR (sqlc.arg(level)::text = 'namespace' AND o.namespace_id = sqlc.arg(namespace_id))
      OR (o.namespace_id = sqlc.arg(namespace_id) AND o.scope_key = sqlc.arg(scope_key))
      OR (sqlc.arg(level)::text = 'collection' AND o.level = 'unit' AND o.namespace_id = sqlc.arg(namespace_id)
          AND EXISTS (
              SELECT 1
              FROM namespaces AS n
              JOIN inventory AS i ON i.namespace = n.name
              JOIN collection_members AS m ON m.unit_id = i.unit_id AND m.namespace = n.name
              WHERE n.id = o.namespace_id AND i.tool = o.tool AND i.name = o.unit_name
                AND m.collection_key = sqlc.arg(collection_key)))
  )
ORDER BY o.scope_key COLLATE "C", o.kind COLLATE "C", o.id COLLATE "C";

-- name: GateUnitCollection :one
-- The key of the collection that holds a unit now. A unit in no collection has
-- no row.
SELECT m.collection_key
FROM namespaces AS n
JOIN inventory AS i ON i.namespace = n.name
JOIN collection_members AS m ON m.unit_id = i.unit_id AND m.namespace = n.name
WHERE n.id = sqlc.arg(namespace_id) AND i.tool = sqlc.arg(tool) AND i.name = sqlc.arg(unit_name);

-- name: ListUnitGateObjectKeys :many
-- The kind, ID and scope key of the configuration of a unit that a purge
-- removes, for the audit entry of the purge.
SELECT kind, id, scope_key
FROM gate_objects
WHERE namespace_id = sqlc.arg(namespace_id) AND level = 'unit' AND tool = sqlc.arg(tool)
  AND unit_name = sqlc.arg(unit_name)
ORDER BY kind, id;

-- name: ListScopeGateObjectKeys :many
-- The kind, ID and scope key of the configuration of one scope that a delete
-- removes, for the audit entry of the delete.
SELECT kind, id, scope_key
FROM gate_objects
WHERE namespace_id = sqlc.arg(namespace_id) AND scope_key = sqlc.arg(scope_key)
ORDER BY kind, id;

-- name: CountUnitGateObjects :one
-- The count of configuration objects that the unit called unit_name holds.
SELECT COUNT(*)
FROM gate_objects
WHERE namespace_id = sqlc.arg(namespace_id) AND level = 'unit' AND tool = sqlc.arg(tool)
  AND unit_name = sqlc.arg(unit_name);

-- name: CopyUnitGateObjects :exec
-- Copies the configuration of a unit to the scope of its new name, in the
-- transaction of a rename.
INSERT INTO gate_objects (namespace_id, scope_key, kind, id, level, tool, unit_name, version, body, updated_at_ms)
SELECT o.namespace_id, CAST(sqlc.arg(new_scope_key) AS TEXT), o.kind, o.id, o.level, o.tool,
       CAST(sqlc.arg(new_unit_name) AS TEXT), o.version, o.body, o.updated_at_ms
FROM gate_objects AS o
WHERE o.namespace_id = sqlc.arg(namespace_id) AND o.level = 'unit' AND o.tool = sqlc.arg(tool)
  AND o.unit_name = sqlc.arg(old_unit_name);

-- name: CopyUnitGateReferences :exec
-- Copies the references of the configuration of a unit to the scope of its
-- new name, in the transaction of a rename.
INSERT INTO gate_references (ref_kind, ref_id, namespace_id, scope_key, owner_kind, owner_id)
SELECT r.ref_kind, r.ref_id, r.namespace_id, CAST(sqlc.arg(new_scope_key) AS TEXT), r.owner_kind, r.owner_id
FROM gate_references AS r
WHERE r.namespace_id = sqlc.arg(namespace_id) AND r.scope_key = sqlc.arg(old_scope_key);

-- name: DeleteUnitGateObjects :exec
-- Deletes the configuration of a unit that a purge removes. The references go
-- with it.
DELETE FROM gate_objects
WHERE namespace_id = sqlc.arg(namespace_id) AND level = 'unit' AND tool = sqlc.arg(tool)
  AND unit_name = sqlc.arg(unit_name);

-- name: DeleteScopeGateObjects :exec
-- Deletes the configuration of one scope, for example a collection that a
-- delete removes. The references go with it.
DELETE FROM gate_objects
WHERE namespace_id = sqlc.arg(namespace_id) AND scope_key = sqlc.arg(scope_key);

-- name: InsertGateRecord :exec
INSERT INTO gate_records (id, namespace_id, point, change_kind, change_id, at_ms, last_at_ms, count, refusal_key, body)
VALUES (sqlc.arg(id), sqlc.arg(namespace_id), sqlc.arg(point), sqlc.arg(change_kind), sqlc.arg(change_id),
        sqlc.arg(at_ms), sqlc.arg(last_at_ms), sqlc.arg(count), sqlc.arg(refusal_key), sqlc.arg(body));

-- name: GetGateRecord :one
SELECT body
FROM gate_records
WHERE id = sqlc.arg(id) AND namespace_id = sqlc.arg(namespace_id);

-- name: ListGateRecordsByChange :many
SELECT body
FROM gate_records
WHERE namespace_id = sqlc.arg(namespace_id) AND change_kind = sqlc.arg(change_kind)
  AND change_id = sqlc.arg(change_id)
ORDER BY at_ms, id COLLATE "C";

-- name: QueryGateRecords :many
-- The records of a decision point with at_ms from from_ms up to, not
-- including, to_ms, newest first.
SELECT body
FROM gate_records
WHERE namespace_id = sqlc.arg(namespace_id) AND point = sqlc.arg(point)
  AND at_ms >= sqlc.arg(from_ms) AND at_ms < sqlc.arg(to_ms)
ORDER BY at_ms DESC, id COLLATE "C" DESC
LIMIT sqlc.arg(max_rows);

-- name: FindGateRefusal :one
-- The newest record of a refusal key whose last refusal is not before since_ms.
SELECT id, count, body
FROM gate_records
WHERE namespace_id = sqlc.arg(namespace_id) AND refusal_key = sqlc.arg(refusal_key) AND refusal_key <> ''
  AND last_at_ms >= sqlc.arg(since_ms)
ORDER BY last_at_ms DESC, id COLLATE "C" DESC
LIMIT 1;

-- name: UpdateGateRefusal :exec
UPDATE gate_records
SET count = sqlc.arg(count), last_at_ms = sqlc.arg(last_at_ms), body = sqlc.arg(body)
WHERE id = sqlc.arg(id);

-- name: LockGateRefusal :exec
-- Takes a lock until the end of the transaction on one refusal key of a
-- namespace, so two refusals of one key at the same time share one record.
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(lock_key)::text, 0));

-- name: GetGateRecordByID :one
-- The namespace of the record with an ID, in any namespace.
SELECT namespace_id
FROM gate_records
WHERE id = sqlc.arg(id);
