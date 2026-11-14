-- name: CreateNamespace :execrows
INSERT INTO namespaces (name, primary_installation, failover_version, created_at_ms, id)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT DO NOTHING;

-- name: GetNamespace :one
SELECT name, primary_installation, failover_version, created_at_ms, allow_forced_writes, allow_unlocked_writes, id
FROM namespaces
WHERE name = $1;

-- name: ListNamespaces :many
SELECT name, primary_installation, failover_version, created_at_ms, allow_forced_writes, allow_unlocked_writes, id
FROM namespaces
ORDER BY name;

-- name: PageNamespaces :many
SELECT name, primary_installation, failover_version, created_at_ms, allow_forced_writes, allow_unlocked_writes, id
FROM namespaces
WHERE name > sqlc.arg('after')
ORDER BY name
LIMIT sqlc.arg('lim');

-- name: PageNamespacesByNames :many
SELECT name, primary_installation, failover_version, created_at_ms, allow_forced_writes, allow_unlocked_writes, id
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

-- name: NamespacesWithList :many
-- Lists the namespaces that have an IP allowlist.
SELECT name
FROM namespaces
WHERE ip_allowlist <> ''
ORDER BY name;

-- name: SetNamespaceIPAllowlist :execrows
-- Replaces the IP allowlist of one namespace (ADR 55, decision 8).
UPDATE namespaces
SET ip_allowlist = $1
WHERE name = $2;

-- name: GetNamespaceOutputSharingDefault :one
SELECT output_sharing_default
FROM namespaces
WHERE name = $1;

-- name: SetNamespaceOutputSharingDefault :execrows
-- Replaces the output sharing setting of one namespace.
UPDATE namespaces
SET output_sharing_default = $1
WHERE name = $2;

-- name: GetNamespaceSourceFilter :one
SELECT source_filter
FROM namespaces
WHERE name = $1;

-- name: SetNamespaceSourceFilter :execrows
-- Replaces the source filter of one namespace.
UPDATE namespaces
SET source_filter = $1
WHERE name = $2;

-- name: GetNamespaceForShare :one
-- Reads the namespace row with a shared lock. A switch change waits for the
-- transaction that reads it, so the switch it reads is the one that commits.
SELECT name, primary_installation, failover_version, created_at_ms, allow_forced_writes, allow_unlocked_writes, id
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
-- The namespace ID comes from the join, so a caller that reads blobs of the
-- stack builds their scope without a second read of the namespace.
SELECT s.namespace, s.project, s.name, s.id, n.id AS namespace_id, s.version, s.created_at_ms, s.updated_at_ms
FROM stacks AS s
JOIN namespaces AS n ON n.name = s.namespace
WHERE s.namespace = $1 AND s.project = $2 AND s.name = $3 AND s.deleted_at_ms IS NULL;

-- name: GetStackWithTags :one
-- GetStack with the tags of the stack, ordered by name, in one statement, so
-- the read needs no transaction. The tag names and values are two arrays in
-- the same order.
SELECT s.namespace, s.project, s.name, s.id, n.id AS namespace_id, s.version, s.created_at_ms, s.updated_at_ms,
       ARRAY(SELECT t.name FROM stack_tags AS t WHERE t.stack_id = s.id ORDER BY t.name)::TEXT[] AS tag_names,
       ARRAY(SELECT t.value FROM stack_tags AS t WHERE t.stack_id = s.id ORDER BY t.name)::TEXT[] AS tag_values
FROM stacks AS s
JOIN namespaces AS n ON n.name = s.namespace
WHERE s.namespace = $1 AND s.project = $2 AND s.name = $3 AND s.deleted_at_ms IS NULL;

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

-- name: ListStackPageInNamespace :many
-- One page of the live stacks of a namespace after a project and a name, in
-- the order of the primary key. The cursor is a range of the key, so the read
-- starts at the cursor. A tag_name that is not empty keeps the stacks that have
-- that tag, and match_value also needs the tag to have the value tag_value.
SELECT s.namespace, s.project, s.name, s.id, s.version, s.created_at_ms, s.updated_at_ms
FROM stacks AS s
WHERE s.namespace = sqlc.arg(namespace) AND s.deleted_at_ms IS NULL
  AND (s.project, s.name) > (sqlc.arg(after_project), sqlc.arg(after_name))
  AND (sqlc.arg(tag_name)::text = '' OR EXISTS (
    SELECT 1 FROM stack_tags AS t
    WHERE t.stack_id = s.id AND t.name = sqlc.arg(tag_name)::text
      AND (NOT sqlc.arg(match_value)::boolean OR t.value = sqlc.arg(tag_value)::text)))
ORDER BY s.project, s.name
LIMIT sqlc.arg(page_limit);

-- name: ListStackPageInProject :many
-- One page of the live stacks of a project after a name, in the order of the
-- primary key, with the tag filter of ListStackPageInNamespace.
SELECT s.namespace, s.project, s.name, s.id, s.version, s.created_at_ms, s.updated_at_ms
FROM stacks AS s
WHERE s.namespace = sqlc.arg(namespace) AND s.project = sqlc.arg(project) AND s.deleted_at_ms IS NULL
  AND s.name > sqlc.arg(after_name)
  AND (sqlc.arg(tag_name)::text = '' OR EXISTS (
    SELECT 1 FROM stack_tags AS t
    WHERE t.stack_id = s.id AND t.name = sqlc.arg(tag_name)::text
      AND (NOT sqlc.arg(match_value)::boolean OR t.value = sqlc.arg(tag_value)::text)))
ORDER BY s.name
LIMIT sqlc.arg(page_limit);

-- name: ListStackTagsOfStacks :many
-- Every tag of the stacks with the IDs, for one page of stacks.
SELECT stack_id, name, value
FROM stack_tags
WHERE stack_id = ANY(sqlc.arg(stack_ids)::text[])
ORDER BY stack_id, name;

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
    created_by, restored_from, token_id, job_id, git_head
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17);

-- name: GetUpdate :one
-- One statement finds the stack by its reference and reads the update, so the
-- read needs no transaction. No stack and no update both return no row.
SELECT u.stack_id, u.id, u.kind, u.dry_run, u.status, u.version, u.revision,
       u.lease_digest, u.lease_expires_at_ms, u.created_at_ms, u.started_at_ms, u.ended_at_ms,
       u.created_by, u.restored_from, u.journal_version, u.token_id, u.job_id, u.git_head,
       COALESCE(c.size, 0)::BIGINT AS checkpoint_bytes
FROM updates AS u
JOIN stacks AS st ON st.id = u.stack_id
LEFT JOIN checkpoints AS c ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE st.namespace = @namespace AND st.project = @project AND st.name = @name AND st.deleted_at_ms IS NULL AND u.id = @id;

-- name: SaveUpdate :execrows
-- A compare-and-swap: it changes nothing unless the stored revision is the
-- one the caller read.
UPDATE updates
SET status = @status, version = @version, revision = @revision,
    lease_digest = @lease_digest, lease_expires_at_ms = @lease_expires_at_ms,
    started_at_ms = @started_at_ms, ended_at_ms = @ended_at_ms,
    journal_version = @journal_version, token_id = @token_id, job_id = @job_id
WHERE stack_id = @stack_id AND id = @id
  AND revision = @expected_revision;

-- name: RunningMutatingUpdate :one
-- The ID of the mutating update that runs on a stack. The unique index
-- updates_one_running holds at most one such row for each stack.
SELECT id
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
-- One statement finds the stack by its reference and reads the update, so the
-- read needs no transaction. No stack and no running update both return no row.
SELECT u.stack_id, u.id, u.kind, u.dry_run, u.status, u.version, u.revision,
       u.lease_digest, u.lease_expires_at_ms, u.created_at_ms, u.started_at_ms, u.ended_at_ms,
       u.created_by, u.restored_from, u.journal_version, u.token_id, u.job_id, u.git_head,
       COALESCE(c.size, 0)::BIGINT AS checkpoint_bytes
FROM updates AS u
JOIN stacks AS st ON st.id = u.stack_id
LEFT JOIN checkpoints AS c ON c.stack_id = u.stack_id AND c.update_id = u.id
WHERE st.namespace = @namespace AND st.project = @project AND st.name = @name AND st.deleted_at_ms IS NULL
  AND u.status = 'running' AND NOT u.dry_run AND u.kind <> 'preview';

-- name: ListUpdates :many
SELECT u.stack_id, u.id, u.kind, u.dry_run, u.status, u.version, u.revision,
       u.lease_digest, u.lease_expires_at_ms, u.created_at_ms, u.started_at_ms, u.ended_at_ms,
       u.created_by, u.restored_from, u.journal_version, u.token_id, u.job_id, u.git_head,
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
-- The namespace ID comes from the join, so a caller reads the scope of the
-- blob of the row in the same call.
SELECT c.stack_id, n.id AS namespace_id, c.update_id, c.lease_digest, c.schema_version, c.sequence,
       c.digest, c.invalid, c.features, c.saved_at_ms,
       c.blob_key, c.size, c.blob_size, c.blob_md5
FROM checkpoints AS c
JOIN updates AS u ON c.stack_id = u.stack_id AND c.update_id = u.id
JOIN stacks AS st ON st.id = c.stack_id
JOIN namespaces AS n ON n.name = st.namespace
WHERE c.stack_id = $1
ORDER BY u.version DESC
LIMIT 1;

-- name: GetLatestCheckpointByRef :one
-- GetLatestCheckpoint for a stack reference: one statement finds the stack
-- and reads its checkpoint, so the read needs no transaction. No stack and no
-- checkpoint both return no row.
SELECT c.stack_id, n.id AS namespace_id, c.update_id, c.lease_digest, c.schema_version, c.sequence,
       c.digest, c.invalid, c.features, c.saved_at_ms,
       c.blob_key, c.size, c.blob_size, c.blob_md5
FROM checkpoints AS c
JOIN updates AS u ON c.stack_id = u.stack_id AND c.update_id = u.id
JOIN stacks AS st ON st.id = c.stack_id
JOIN namespaces AS n ON n.name = st.namespace
WHERE st.namespace = @namespace AND st.project = @project AND st.name = @name AND st.deleted_at_ms IS NULL
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
SELECT c.stack_id, n.id AS namespace_id, c.update_id, c.lease_digest, c.schema_version, c.sequence,
       c.digest, c.invalid, c.features, c.saved_at_ms,
       c.blob_key, c.size, c.blob_size, c.blob_md5
FROM checkpoints AS c
JOIN stacks AS st ON st.id = c.stack_id
JOIN namespaces AS n ON n.name = st.namespace
WHERE c.stack_id = $1 AND c.update_id = $2;

-- name: GetCheckpointByUpdateRef :one
-- GetCheckpointByUpdate for a stack reference: one statement finds the stack
-- and reads the checkpoint, so the read needs no transaction. No stack and no
-- checkpoint both return no row.
SELECT c.stack_id, n.id AS namespace_id, c.update_id, c.lease_digest, c.schema_version, c.sequence,
       c.digest, c.invalid, c.features, c.saved_at_ms,
       c.blob_key, c.size, c.blob_size, c.blob_md5
FROM checkpoints AS c
JOIN stacks AS st ON st.id = c.stack_id
JOIN namespaces AS n ON n.name = st.namespace
WHERE st.namespace = @namespace AND st.project = @project AND st.name = @name AND st.deleted_at_ms IS NULL AND c.update_id = @update_id;

-- name: GetCheckpointByVersion :one
-- One statement finds the stack by its reference and reads the checkpoint, so
-- the read needs no transaction. No stack and no checkpoint both return no row.
SELECT c.stack_id, n.id AS namespace_id, c.update_id, c.lease_digest, c.schema_version, c.sequence,
       c.digest, c.invalid, c.features, c.saved_at_ms,
       c.blob_key, c.size, c.blob_size, c.blob_md5
FROM checkpoints AS c
JOIN updates AS u ON c.stack_id = u.stack_id AND c.update_id = u.id
JOIN stacks AS st ON st.id = c.stack_id
JOIN namespaces AS n ON n.name = st.namespace
WHERE st.namespace = @namespace AND st.project = @project AND st.name = @name AND st.deleted_at_ms IS NULL AND u.version = @version;

-- name: PutCheckpoint :one
-- The row of the update decides which write wins. The conflict update runs only
-- for a write without a sequence number, or for a sequence above the stored one,
-- and the database checks that against the latest committed row, so two writers
-- of one sequence cannot both land. No row means that the write lost.
-- The row that the write returns holds the time of the database clock after the
-- write, and so after every lock wait of the statement. The caller checks the
-- attempt window with it, so the check needs no round trip of its own.
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
    blob_md5 = excluded.blob_md5
WHERE excluded.sequence = 0 OR checkpoints.sequence < excluded.sequence
RETURNING FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint AS now_ms;

-- name: ListReferencedBlobKeys :many
-- Every blob key that a row of one live stack names, ordered, without
-- duplicates: the blobs of the checkpoints and the blobs of the journal
-- batches. A sweeper compares this set against the stored objects to find
-- blobs that no row references. The stack is the driving table, so one
-- statement tells a stack without keys (one row with an empty key) from a
-- stack that is missing or soft-deleted (no row), and the read needs no
-- transaction.
SELECT CAST(COALESCE(k.blob_key, '') AS TEXT) AS blob_key
FROM stacks AS st
LEFT JOIN (
    SELECT c.stack_id, c.blob_key FROM checkpoints AS c
    UNION
    SELECT j.stack_id, j.blob_key FROM update_journal AS j
) AS k ON k.stack_id = st.id
WHERE st.id = @stack_id AND st.deleted_at_ms IS NULL
ORDER BY 1;

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
-- One statement finds the stack by its reference, checks the update and reads
-- the page of events, so the read needs no transaction. No stack and no update
-- both return no row. An update with no event in the page returns one row
-- with has_event false.
SELECT (e.sequence IS NOT NULL)::BOOLEAN AS has_event,
       COALESCE(e.sequence, 0)::BIGINT AS sequence,
       COALESCE(e.timestamp_ms, 0)::BIGINT AS timestamp_ms,
       COALESCE(e.payload, ''::BYTEA)::BYTEA AS payload
FROM updates AS u
JOIN stacks AS st ON st.id = u.stack_id
LEFT JOIN LATERAL (
    SELECT ev.sequence, ev.timestamp_ms, ev.payload
    FROM update_events AS ev
    WHERE ev.stack_id = u.stack_id AND ev.update_id = u.id AND ev.sequence > @after
    ORDER BY ev.sequence
    LIMIT @page_limit
) AS e ON TRUE
WHERE st.namespace = @namespace AND st.project = @project AND st.name = @name AND st.deleted_at_ms IS NULL AND u.id = @id
ORDER BY e.sequence;

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

-- name: UpdateInventoryLastRelease :exec
-- Publishes the last released lock of one workspace unit. The caller writes it
-- in the same transaction as the record on the workspace. An empty kind, empty
-- text and the time 0 mean no record.
UPDATE inventory
SET last_release_lock_id = $1, last_release_holder = $2, last_release_by = $3,
    last_release_kind = $4, last_release_at_ms = $5
WHERE namespace = $6 AND tool = $7 AND name = $8;

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

-- name: DeleteInventoryLabels :exec
-- Removes the labels of one unit by its stable ID.
DELETE FROM inventory_labels
WHERE unit_id = $1;

-- name: DeleteInventoryLabelsByName :exec
-- Removes the labels of the unit that the live entry names. It runs before
-- DeleteInventory removes the entry.
DELETE FROM inventory_labels
WHERE unit_id IN (
    SELECT i.unit_id FROM inventory AS i WHERE i.namespace = $1 AND i.tool = $2 AND i.name = $3
);

-- name: CopyStackTagsToInventoryLabels :exec
-- Writes the tags of one stack as the labels of its unit. The caller removes
-- the old labels first, in the same transaction.
INSERT INTO inventory_labels (namespace, unit_id, label_key, label_value)
SELECT s.namespace, t.stack_id, t.name, t.value
FROM stack_tags AS t
JOIN stacks AS s ON s.id = t.stack_id
WHERE t.stack_id = $1;

-- name: ListInventoryLabelsOfUnits :many
-- Every label of the live units with the IDs, for one page of the unit list.
SELECT unit_id, label_key, label_value
FROM inventory_labels
WHERE unit_id = ANY(sqlc.arg(unit_ids)::text[])
ORDER BY unit_id, label_key;

-- name: ListInventoryLabels :many
-- Every label of the live units of a namespace.
SELECT unit_id, label_key, label_value
FROM inventory_labels
WHERE namespace = $1
ORDER BY unit_id, label_key;

-- name: AppendInventoryChange :one
-- Appends one row to the change log of the namespace that has the name now, in
-- one statement: it resolves the namespace, takes the next number of the log,
-- and writes the row with the name of the collection that holds the unit. No
-- row comes back when the namespace does not exist. The statement runs last in
-- the transaction of the write, after every other row lock, so the order of the
-- numbers is the order of the commits of the namespace.
WITH ns AS (
    SELECT id FROM namespaces WHERE namespaces.name = sqlc.arg(namespace_name)
), head AS (
    INSERT INTO inventory_change_heads (namespace_id, last_seq)
    SELECT id, 1 FROM ns
    ON CONFLICT (namespace_id) DO UPDATE SET last_seq = inventory_change_heads.last_seq + 1
    RETURNING namespace_id, last_seq
)
INSERT INTO inventory_changes (
    namespace_id, seq, at_ms, tool, unit_id, name, display_name, status, collection, kind, resource_count
)
SELECT
    head.namespace_id, head.last_seq, sqlc.arg(at_ms)::bigint, sqlc.arg(tool)::text, sqlc.arg(unit_id)::text,
    sqlc.arg(unit_name)::text, sqlc.arg(display_name)::text, sqlc.arg(status)::text,
    COALESCE((
        SELECT c.name
        FROM collection_members AS m
        JOIN collections AS c ON c.namespace = m.namespace AND c.name_key = m.collection_key
        WHERE m.unit_id = sqlc.arg(unit_id)::text
    ), '')::text,
    sqlc.arg(kind)::text, NULL
FROM head
RETURNING seq;

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
       i.last_release_lock_id, i.last_release_holder, i.last_release_by, i.last_release_kind,
       i.last_release_at_ms,
       COALESCE(c.name, '') AS collection,
       ul.holder AS unit_manual_holder, ul.reason AS unit_manual_reason, ul.since_ms AS unit_manual_since_ms,
       cl.holder AS collection_manual_holder, cl.reason AS collection_manual_reason,
       cl.since_ms AS collection_manual_since_ms,
       nl.holder AS namespace_manual_holder, nl.reason AS namespace_manual_reason,
       nl.since_ms AS namespace_manual_since_ms
FROM inventory AS i
LEFT JOIN collection_members AS m ON m.unit_id = i.unit_id
LEFT JOIN collections AS c ON c.namespace = m.namespace AND c.name_key = m.collection_key
LEFT JOIN manual_locks AS ul ON ul.kind = 'unit' AND ul.object_id = i.unit_id
LEFT JOIN manual_locks AS cl ON cl.kind = 'collection' AND cl.object_id = c.id
LEFT JOIN namespaces AS ns ON ns.name = i.namespace
LEFT JOIN manual_locks AS nl ON nl.kind = 'namespace' AND nl.object_id = ns.id
WHERE i.namespace = $1
ORDER BY i.tool, i.name;

-- name: GetInventoryEntryByUnitID :one
-- One live unit by its stable ID, which is unique in the installation.
SELECT i.namespace, i.tool, i.name, i.unit_id, i.display_name, i.last_changed_ms, i.status, i.resource_count,
       i.lock_holder, i.lock_since_ms, i.lock_operation,
       i.last_release_lock_id, i.last_release_holder, i.last_release_by, i.last_release_kind,
       i.last_release_at_ms,
       COALESCE(c.name, '') AS collection,
       ul.holder AS unit_manual_holder, ul.reason AS unit_manual_reason, ul.since_ms AS unit_manual_since_ms,
       cl.holder AS collection_manual_holder, cl.reason AS collection_manual_reason,
       cl.since_ms AS collection_manual_since_ms,
       nl.holder AS namespace_manual_holder, nl.reason AS namespace_manual_reason,
       nl.since_ms AS namespace_manual_since_ms
FROM inventory AS i
LEFT JOIN collection_members AS m ON m.unit_id = i.unit_id
LEFT JOIN collections AS c ON c.namespace = m.namespace AND c.name_key = m.collection_key
LEFT JOIN manual_locks AS ul ON ul.kind = 'unit' AND ul.object_id = i.unit_id
LEFT JOIN manual_locks AS cl ON cl.kind = 'collection' AND cl.object_id = c.id
LEFT JOIN namespaces AS ns ON ns.name = i.namespace
LEFT JOIN manual_locks AS nl ON nl.kind = 'namespace' AND nl.object_id = ns.id
WHERE i.unit_id = $1;

-- name: ListInventoryUnitLabels :many
-- Every label of one live unit.
SELECT label_key, label_value
FROM inventory_labels
WHERE unit_id = $1
ORDER BY label_key;

-- name: CreateWorkspace :execrows
INSERT INTO workspaces (
    id, namespace, name, revision, lock_id, lock_holder, lock_reason,
    locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms,
    allow_forced_writes, allow_unlocked_writes, last_release_lock_id, last_release_holder,
    last_release_by, last_release_kind, last_release_at_ms
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
ON CONFLICT DO NOTHING;

-- name: GetWorkspace :one
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes,
       deleted_at_ms, deleted_name,
       last_release_lock_id, last_release_holder, last_release_by, last_release_kind, last_release_at_ms
FROM workspaces
WHERE namespace = $1 AND name = $2 AND deleted_at_ms IS NULL;

-- name: GetWorkspaceByID :one
-- The live check is num_nulls(deleted_at_ms) = 1, not IS NULL, so the
-- partial index workspaces_live_name cannot serve this lookup by ID.
-- Only NULL reads as live: a stored -1 or 0 does not, as with IS NULL.
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes,
       deleted_at_ms, deleted_name,
       last_release_lock_id, last_release_holder, last_release_by, last_release_kind, last_release_at_ms
FROM workspaces
WHERE id = $1 AND num_nulls(deleted_at_ms) = 1;

-- name: ReadWorkspace :one
-- The read behind the store port: the workspace with the ID of its namespace,
-- from the join with the namespace row, so a caller needs no second read.
SELECT sqlc.embed(w), n.id AS namespace_id
FROM workspaces AS w
JOIN namespaces AS n ON n.name = w.namespace
WHERE w.namespace = $1 AND w.name = $2 AND w.deleted_at_ms IS NULL;

-- name: ReadWorkspaceByID :one
-- The live check is num_nulls(deleted_at_ms) = 1, not IS NULL, so the
-- partial index workspaces_live_name cannot serve this lookup by ID.
-- Only NULL reads as live: a stored -1 or 0 does not, as with IS NULL.
SELECT sqlc.embed(w), n.id AS namespace_id
FROM workspaces AS w
JOIN namespaces AS n ON n.name = w.namespace
WHERE w.id = $1 AND num_nulls(w.deleted_at_ms) = 1;

-- name: ReadWorkspaces :many
SELECT sqlc.embed(w), n.id AS namespace_id
FROM workspaces AS w
JOIN namespaces AS n ON n.name = w.namespace
WHERE w.namespace = $1 AND w.deleted_at_ms IS NULL
ORDER BY w.name;

-- name: CountWorkspacePage :one
-- The count of the live workspaces of a namespace whose name holds the
-- search, for the total of a workspace page.
SELECT COUNT(*)
FROM workspaces AS w
WHERE w.namespace = sqlc.arg(namespace) AND w.deleted_at_ms IS NULL AND strpos(w.name, sqlc.arg(name_contains)) > 0;

-- name: ReadWorkspacePage :many
-- One page of the live workspaces of a namespace whose name holds the search,
-- in the order of ReadWorkspaces. The inner query finds the names of the page
-- in the index of the live workspace names alone, so an offset skips index
-- entries and no table rows. The outer query reads the rows of the page by
-- name = ANY of that array, so the names are in the index condition of each
-- plan. A join on the names let a plan with no table statistics read all the
-- live workspaces of the namespace for each name of the page.
SELECT sqlc.embed(w), n.id AS namespace_id
FROM workspaces AS w
JOIN namespaces AS n ON n.name = w.namespace
WHERE w.namespace = sqlc.arg(namespace) AND w.deleted_at_ms IS NULL
    AND w.name = ANY (ARRAY(
        SELECT p.name
        FROM workspaces AS p
        WHERE p.namespace = sqlc.arg(namespace) AND p.deleted_at_ms IS NULL AND strpos(p.name, sqlc.arg(name_contains)) > 0
        ORDER BY p.name
        LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset)
    ))
ORDER BY w.name;

-- name: LockWorkspaceForFinalize :one
-- Locks the workspace row so two finalizes on one workspace cannot both pass
-- the serial check.
-- The live check is num_nulls(deleted_at_ms) = 1, not IS NULL, so the
-- partial index workspaces_live_name cannot serve this lookup by ID.
-- Only NULL reads as live: a stored -1 or 0 does not, as with IS NULL.
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes,
       deleted_at_ms, deleted_name,
       last_release_lock_id, last_release_holder, last_release_by, last_release_kind, last_release_at_ms
FROM workspaces
WHERE id = $1 AND num_nulls(deleted_at_ms) = 1
FOR UPDATE;

-- name: ListWorkspaces :many
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes,
       deleted_at_ms, deleted_name,
       last_release_lock_id, last_release_holder, last_release_by, last_release_kind, last_release_at_ms
FROM workspaces
WHERE namespace = $1 AND deleted_at_ms IS NULL
ORDER BY name;

-- name: SaveWorkspace :execrows
-- A compare-and-swap: it changes nothing unless the stored revision is the
-- one the caller read. The current state pointer stays: only FinalizeStateVersion moves it.
UPDATE workspaces
SET revision = @revision, lock_id = @lock_id, lock_holder = @lock_holder,
    lock_reason = @lock_reason, locked_at_ms = @locked_at_ms, updated_at_ms = @updated_at_ms,
    allow_forced_writes = @allow_forced_writes, allow_unlocked_writes = @allow_unlocked_writes,
    last_release_lock_id = @last_release_lock_id, last_release_holder = @last_release_holder,
    last_release_by = @last_release_by, last_release_kind = @last_release_kind,
    last_release_at_ms = @last_release_at_ms
WHERE id = @id AND revision = @expected_revision;

-- name: WorkspaceExists :one
-- The live check is num_nulls(deleted_at_ms) = 1, not IS NULL, so the
-- partial index workspaces_live_name cannot serve this lookup by ID.
-- Only NULL reads as live: a stored -1 or 0 does not, as with IS NULL.
SELECT EXISTS (
    SELECT 1 FROM workspaces WHERE id = $1 AND num_nulls(deleted_at_ms) = 1
);

-- name: SoftDeleteWorkspace :execrows
-- Moves the name to deleted_name and frees it (ADR 47). A workspace name
-- cannot hold ":", so "deleted:" and the ID never match a lookup by name. The
-- revision moves on, so a concurrent compare-and-swap of the row fails.
-- The live check is num_nulls(deleted_at_ms) = 1, not IS NULL, so the
-- partial index workspaces_live_name cannot serve this lookup by ID.
-- Only NULL reads as live: a stored -1 or 0 does not, as with IS NULL.
UPDATE workspaces
SET deleted_name = name, name = 'deleted:' || id, deleted_at_ms = $1, revision = revision + 1
WHERE id = $2 AND num_nulls(deleted_at_ms) = 1;

-- name: DeleteStateVersionsOfWorkspace :exec
-- Removes every state version of one workspace, pending and finalized.
DELETE FROM state_versions
WHERE workspace_id = $1;

-- name: CreateStateVersion :exec
INSERT INTO state_versions (
    id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
    force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
    restored_from, sealed, sensitive_marks, fenced_by, unlocked_write, origin_job
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22
);

-- name: GetStateVersion :one
SELECT id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
       force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
       restored_from, sealed, sensitive_marks, fenced_by, unlocked_write, origin_job
FROM state_versions
WHERE id = $1;

-- name: ListStateVersions :many
-- Finalized versions of one workspace, newest first. Ties on finalize time
-- break by ID, as the port comment promises. The caller normalizes the page.
SELECT id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
       force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
       restored_from, sealed, sensitive_marks, fenced_by, unlocked_write, origin_job
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
-- The live check is num_nulls(deleted_at_ms) = 1, not IS NULL, so the
-- partial index workspaces_live_name cannot serve this lookup by ID.
-- Only NULL reads as live: a stored -1 or 0 does not, as with IS NULL.
SELECT id, namespace, name, revision, lock_id, lock_holder, lock_reason,
       locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms, allow_forced_writes, allow_unlocked_writes,
       deleted_at_ms, deleted_name,
       last_release_lock_id, last_release_holder, last_release_by, last_release_kind, last_release_at_ms
FROM workspaces
WHERE id = $1 AND num_nulls(deleted_at_ms) = 1
FOR UPDATE;

-- name: ListStateContentKeys :many
-- Every blob key the versions of one workspace name, without the empty ones.
-- The workspace is the driving table, so one statement tells a workspace
-- without keys (one row with an empty key) from a workspace that does not
-- exist (no row). A soft-deleted workspace still has its row and its keys.
SELECT CAST(COALESCE(k.content_key, '') AS TEXT) AS content_key
FROM workspaces AS w
LEFT JOIN state_versions AS k ON k.workspace_id = w.id AND k.content_key <> ''
WHERE w.id = $1;

-- name: ListExpiredPendingStateVersions :many
-- Expired pending versions of one workspace, oldest expiry first. Ties on
-- expiry break by ID, as the port comment promises.
SELECT id, workspace_id, status, serial, lineage, md5, size, content_key, outputs,
       force, upload_digest, upload_expires_at_ms, holder, lock_id, created_at_ms, finalized_at_ms,
       restored_from, sealed, sensitive_marks, fenced_by, unlocked_write, origin_job
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
       sv.sensitive_marks, sv.fenced_by, unlocked_write, origin_job
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

-- name: RunningWork :one
-- Reads one backup in the running state, or else one restore in the checking
-- or applying state, in one statement. The guard of the blob deletes uses it.
-- A backup comes first, and the smaller ID comes first, so the answer is the
-- same for the same rows.
SELECT kind, id
FROM (
    SELECT 'backup'::text AS kind, id FROM backups WHERE state = 'running'
    UNION ALL
    SELECT 'restore'::text AS kind, id FROM restores WHERE state IN ('checking', 'applying')
) AS running_work
ORDER BY kind, id
LIMIT 1;

-- name: ListBackups :many
SELECT id, state, started_at_ms, ended_at_ms, target, error
FROM backups
ORDER BY started_at_ms DESC, id ASC;

-- name: FinishBackup :execrows
UPDATE backups
SET state = $1, error = $2, ended_at_ms = $3
WHERE id = $4 AND state = 'running';

-- name: FailEndedBackups :execrows
-- Marks failed each running backup whose operation ended failed or canceled
-- without a finish write. The error text is the last error of the
-- operation, or the fallback when it holds none.
UPDATE backups AS b
SET state = 'failed',
    error = COALESCE(NULLIF(o.last_error, ''), sqlc.arg('fallback')::text),
    ended_at_ms = sqlc.arg('ended_at_ms')
FROM durable_operations AS o
WHERE b.state = 'running'
  AND o.start_key = sqlc.arg('prefix')::text || b.id
  AND o.kind = sqlc.arg('kind')
  AND o.state IN ('failed', 'canceled');

-- name: LockInstallationState :one
-- The installation state, with the row locked FOR UPDATE until the end of the
-- transaction, so a leave of maintenance mode waits for the swap of a
-- restore that checked the state.
SELECT state
FROM installation
WHERE id = 1
FOR UPDATE;

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

-- name: RecordMaintenanceRevocation :exec
-- Journals one write that only reduces access, when the installation is in
-- maintenance mode, and nothing otherwise. A restore applies the journal
-- again after the swap. The read of the state holds the installation row FOR
-- SHARE until the transaction ends. An enter updates that row, so it waits
-- for a revocation in flight, and a revocation that starts after the enter
-- reads maintenance: no revocation commits in maintenance mode without its
-- journal row.
INSERT INTO maintenance_revocations (kind, target, end_reason, actor, object, at_ms)
SELECT sqlc.arg('kind')::text, sqlc.arg('target')::text, sqlc.arg('end_reason')::text, sqlc.arg('actor')::text,
       sqlc.arg('object')::text, sqlc.arg('at_ms')::bigint
WHERE (SELECT state FROM installation WHERE id = 1 FOR SHARE) = 'maintenance';

-- name: ClearMaintenanceRevocationsOnEnter :exec
-- Starts a new stay in maintenance mode: it clears the journal when the stored
-- state is not maintenance yet. Run it before the state changes.
DELETE FROM maintenance_revocations
WHERE (SELECT state FROM installation WHERE id = 1) <> 'maintenance';

-- name: SetLockTimeout :exec
-- Limits the wait for a lock in this transaction, as SET LOCAL lock_timeout.
-- The timeout is a PostgreSQL duration text, for example 10000ms.
SELECT set_config('lock_timeout', sqlc.arg(timeout)::text, true);

-- name: LockWritesExclusive :exec
-- Takes the exclusive form of the write lock for this transaction. It waits
-- for every write transaction in flight, and holds every later write back
-- until this transaction ends. Each write transaction takes the shared form.
SELECT pg_advisory_xact_lock(sqlc.arg(class)::int, hashtext(current_schema()));

-- name: GetInstallationKind :one
-- The installation kind: dev, production or NULL before the first claim. The
-- migration seeds the one row; a missing row is a fault.
SELECT kind
FROM installation
WHERE id = 1;

-- name: ClaimInstallationKind :execrows
-- Stores the kind in the seeded row only when no kind is stored: a
-- compare-and-set, so several instances on one new database all claim and one
-- wins. It changes nothing when a kind is stored or the row is missing.
UPDATE installation
SET kind = $1
WHERE id = 1 AND kind IS NULL;

-- name: InsertRestore :execrows
-- Stores one checking restore. The unique index on the running restores, and
-- the primary key, make a second running restore or a taken ID match no row.
INSERT INTO restores (id, state, backup_id, pre_backup_id, requested_by, started_at_ms, ended_at_ms, error, dev_report)
VALUES ($1, 'checking', $2, '', $3, $4, NULL, '', $5)
ON CONFLICT DO NOTHING;

-- name: GetRestore :one
SELECT id, state, backup_id, pre_backup_id, requested_by, started_at_ms, ended_at_ms, error, dev_report
FROM restores
WHERE id = $1;

-- name: ListRestores :many
SELECT id, state, backup_id, pre_backup_id, requested_by, started_at_ms, ended_at_ms, error, dev_report
FROM restores
ORDER BY started_at_ms DESC, id ASC;

-- name: GetRestoreMaintenance :one
-- Reads the state of one restore and whether it holds the maintenance mode
-- that it entered.
SELECT state, maintenance_entered
FROM restores
WHERE id = $1;

-- name: EndRestore :execrows
-- Moves one running restore to its end state with its final dev report, and
-- clears its maintenance flag. A finished restore matches no row.
UPDATE restores
SET state = $1, error = $2, ended_at_ms = $3, dev_report = $4, maintenance_entered = false
WHERE id = $5 AND state IN ('checking', 'applying');

-- name: MarkRestoreApplying :execrows
-- Moves one checking restore to applying with the backup taken before the
-- apply. Any other state matches no row, so a retry reports no change.
UPDATE restores
SET state = 'applying', pre_backup_id = $1
WHERE id = $2 AND state = 'checking';

-- name: SetRestoreDevReport :execrows
-- Stores the report of the dev data of one running restore, with the codes
-- of its warnings. A restore that does not run matches no row.
UPDATE restores
SET dev_report = $1
WHERE id = $2 AND state IN ('checking', 'applying');

-- name: GetRestoreAuditEvents :one
-- Reads the state of one restore and the object texts of its audit events.
SELECT state, audit_events
FROM restores
WHERE id = $1;

-- name: SetRestoreAuditEvents :execrows
-- Stores the audit events of one running restore. A restore that does not
-- run matches no row.
UPDATE restores
SET audit_events = $1
WHERE id = $2 AND state IN ('checking', 'applying');

-- name: SetRestoreMaintenanceEntered :execrows
-- Sets whether one running restore holds the maintenance mode that it
-- entered. A restore that does not run matches no row.
UPDATE restores
SET maintenance_entered = $1
WHERE id = $2 AND state IN ('checking', 'applying');

-- name: BumpFailoverVersions :execrows
-- Increments the failover version of every namespace by one (ADR 38
-- decision 7), so leases and locks from before the restore stop working.
UPDATE namespaces
SET failover_version = failover_version + 1;

-- name: BackupPrimaryInstallations :many
SELECT DISTINCT primary_installation
FROM namespaces
ORDER BY primary_installation;

-- name: BackupWrapperIDs :many
-- The wrapper IDs of the namespace keys and of the installation keys, so the
-- restore check finds the key-encryption key of each.
SELECT wrapper_id FROM secret_keys
UNION
SELECT wrapper_id FROM installation_secret_keys
ORDER BY wrapper_id;

-- name: BackupBlobs :many
-- Every blob key the finalized state versions, the checkpoints, the journal
-- batches and the item keys that are not shredded name, with the size
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
UNION ALL
SELECT 'namespaces/' || k.namespace_id || '/item-keys/' || k.id || '/v' || k.blob_version::text AS content_key,
       k.blob_size AS size, k.blob_md5 AS md5, FALSE AS sealed
FROM item_keys AS k
WHERE k.shredded_at_ms = 0
ORDER BY content_key;

-- name: GetAuditEntry :one
-- The entry of a scope at one sequence, for the check of signed
-- checkpoints. A missing entry matches no row.
SELECT namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
       at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count, changes, object_id,
       fence_version, reason, principal_kind, job_id
FROM audit_entries
WHERE namespace = $1 AND sequence = $2;

-- name: LastAuditEntry :one
-- The entry with the highest sequence of a namespace, for the next append.
-- A namespace with no entries matches no row.
SELECT namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
       at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count, changes, object_id,
       fence_version, reason, principal_kind, job_id
FROM audit_entries
WHERE namespace = $1
ORDER BY sequence DESC
LIMIT 1;

-- name: InsertAuditEntry :exec
INSERT INTO audit_entries (
    namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
    at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count, changes, object_id,
    fence_version, reason, principal_kind, job_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25);

-- name: ListAuditEntries :many
-- One page of the entries of a namespace, newest first. Ties on time break
-- by sequence, as the port comment promises.
SELECT namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
       at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count, changes, object_id,
       fence_version, reason, principal_kind, job_id
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
       fence_version, reason, principal_kind, job_id
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

-- name: JournalBatchRefs :many
SELECT blob_key, sha256, size FROM update_journal
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
-- The live check is num_nulls(deleted_at_ms) = 1, not IS NULL, so no
-- partial index of the live rows can serve this lookup by ID.
-- Only NULL reads as live: a stored -1 or 0 does not, as with IS NULL.
UPDATE principals SET display_name = $1, updated_at_ms = $2
WHERE id = $3 AND namespace = $4 AND num_nulls(deleted_at_ms) = 1;

-- name: DeletePrincipal :execrows
-- The live check is num_nulls(deleted_at_ms) = 1, not IS NULL, so no
-- partial index of the live rows can serve this lookup by ID.
-- Only NULL reads as live: a stored -1 or 0 does not, as with IS NULL.
-- An enabled break-glass account stays while no other enabled break-glass
-- account exists. A disabled account is not the last enabled one, so its end
-- is allowed. The condition alone does not stop two writes at the same time:
-- under READ COMMITTED each statement sees the other account as enabled, and
-- both succeed. The caller must hold the advisory lock of the server audit
-- scope (LockServerAuditScope) first. The lock serializes the writes, and the
-- statement of the second one reads the commit of the first.
UPDATE principals SET deleted_at_ms = $1
WHERE principals.id = $2 AND principals.namespace = $3 AND num_nulls(principals.deleted_at_ms) = 1
  AND (NOT principals.break_glass OR principals.disabled_at_ms IS NOT NULL OR EXISTS (
    SELECT 1 FROM principals o
    WHERE o.id <> principals.id AND o.break_glass AND o.kind = 'person' AND o.namespace = ''
      AND o.deleted_at_ms IS NULL AND o.disabled_at_ms IS NULL
  ));

-- name: CountEnabledBreakGlassPrincipal :one
-- 1 when the principal is an enabled break-glass account of the
-- installation, so a write that changed no row of it was the refusal of the
-- last one.
SELECT COUNT(*) FROM principals
WHERE id = $1 AND namespace = '' AND break_glass AND deleted_at_ms IS NULL AND disabled_at_ms IS NULL;

-- name: EndMembershipsOfPrincipal :exec
UPDATE group_members SET removed_at_ms = $1
WHERE principal_id = $2 AND removed_at_ms IS NULL;

-- name: CreateGroup :execrows
INSERT INTO principal_groups (id, name, name_key, description, source, created_at_ms, updated_at_ms, closed_to_identity_provider)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT DO NOTHING;

-- name: GetGroup :one
SELECT g.id, g.name, g.description, g.source, g.created_at_ms, g.updated_at_ms, g.closed_to_identity_provider,
  (SELECT COUNT(*) FROM group_members m WHERE m.group_id = g.id AND m.removed_at_ms IS NULL AND m.state = 'active') AS member_count
FROM principal_groups g WHERE g.id = $1 AND g.deleted_at_ms IS NULL;

-- name: GroupIDByNameKey :one
SELECT id FROM principal_groups WHERE name_key COLLATE "C" = @name_key::text AND deleted_at_ms IS NULL;

-- name: ListGroups :many
SELECT g.id, g.name, g.description, g.source, g.created_at_ms, g.updated_at_ms, g.closed_to_identity_provider,
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
UPDATE principal_groups SET name = $1, name_key = $2, description = $3, closed_to_identity_provider = $4, updated_at_ms = $5
WHERE id = $6 AND deleted_at_ms IS NULL;

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

-- name: RestoreGroupMember :execrows
-- Undoes RemoveGroupMember in the same transaction, for a removal that the
-- last-site-administrator rule refuses.
UPDATE group_members SET removed_at_ms = NULL
WHERE group_id = $1 AND principal_id = $2 AND removed_at_ms = $3;

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

-- name: ListActiveGroupNamesOfPrincipal :many
SELECT g.name
FROM group_members m JOIN principal_groups g ON g.id = m.group_id
WHERE m.principal_id = @principal_id AND m.removed_at_ms IS NULL
  AND m.state = 'active' AND g.deleted_at_ms IS NULL
GROUP BY g.name, g.name_key
ORDER BY g.name_key COLLATE "C";

-- name: ListGroupsOfPrincipal :many
SELECT g.id, g.name, g.description, g.source, g.created_at_ms, g.updated_at_ms, g.closed_to_identity_provider,
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

-- name: LockNamespaceCreatesShared :exec
-- A create of a workspace takes this lock, in the shared form, before it reads
-- the manual lock of its namespace. The put of a manual lock on a namespace
-- takes the exclusive form. So a lock that commits while a create runs is
-- either read by the create or waits until the create ends: no create commits
-- under a lock that it did not read. The key holds the schema, so databases that
-- hold several schemas do not wait for each other.
SELECT pg_advisory_xact_lock_shared(
  hashtextextended('namespace-creates:' || current_schema() || ':' || sqlc.arg(namespace)::text, 0));

-- name: LockNamespaceCreatesExclusive :exec
-- Takes the exclusive form of the lock of LockNamespaceCreatesShared.
SELECT pg_advisory_xact_lock(
  hashtextextended('namespace-creates:' || current_schema() || ':' || sqlc.arg(namespace)::text, 0));

-- name: TryLockSweep :one
-- Takes the session lock of the retention sweep without a wait. It answers
-- false when a session of any server process holds it. The second key is the
-- schema, so two installations in one database do not wait for each other.
SELECT pg_try_advisory_lock(sqlc.arg(class)::int, hashtext(current_schema()))::boolean AS held;

-- name: UnlockSweep :one
-- Gives up the session lock of TryLockSweep. It answers false when the
-- session does not hold the lock.
SELECT pg_advisory_unlock(sqlc.arg(class)::int, hashtext(current_schema()))::boolean AS unlocked;

-- name: LockPasskeySignInChallenges :exec
-- Orders the starts of passkey sign-ins. A start counts the open challenges of
-- its source and of the installation, and then inserts its row. Without this
-- lock, starts at the same time in READ COMMITTED all count the same rows and
-- go over the caps. The lock holds until the end of the transaction. The key
-- holds the schema, so databases that hold several schemas do not wait for
-- each other.
SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':passkey sign-in challenges', 0));

-- name: LockServerAuditScope :exec
-- Orders the appends of the audit scope @server, which has no namespace row
-- to lock. The key is fixed: 0x6175646974 is "audit" in ASCII.
SELECT pg_advisory_xact_lock(418581342580);

-- name: PutGroupMapping :execrows
INSERT INTO group_mappings (source, source_id, claim_value, group_id, created_by, created_at_ms)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT DO NOTHING;

-- name: DeleteGroupMapping :execrows
DELETE FROM group_mappings
WHERE source = $1 AND source_id = $2 AND claim_value = $3 AND group_id = $4;

-- name: ListGroupMappings :many
SELECT source, source_id, claim_value, group_id, created_by, created_at_ms
FROM group_mappings
WHERE source = $1 AND source_id = $2
ORDER BY claim_value COLLATE "C", group_id COLLATE "C";

-- name: ListLiveGroupMappings :many
-- The mappings of the source and the source ID whose group exists and was
-- not deleted. A mapping of a deleted group stays in the table for the record
-- and never answers here.
SELECT m.claim_value, m.group_id
FROM group_mappings m JOIN principal_groups g ON g.id = m.group_id
WHERE m.source = $1 AND m.source_id = $2 AND g.deleted_at_ms IS NULL
ORDER BY m.claim_value COLLATE "C", m.group_id COLLATE "C";

-- name: ListCurrentMembershipsOfPrincipal :many
-- Each current membership of the principal, whatever its source, so that a
-- group claim changes only the memberships of its own source and source ID.
SELECT group_id, source, source_id, state, pinned
FROM group_members
WHERE principal_id = $1 AND removed_at_ms IS NULL
ORDER BY group_id COLLATE "C";

-- name: AddMemberRemoval :exec
INSERT INTO member_removals (id, source_kind, source_id, group_id, removed, members_before, at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: MarkMemberRemovalAlert :exec
INSERT INTO member_removal_alerts (source_kind, source_id, alerted_at_ms)
VALUES ($1, $2, $3)
ON CONFLICT (source_kind, source_id) DO UPDATE SET alerted_at_ms = excluded.alerted_at_ms;

-- name: MemberRemovalAlertedAt :one
-- The time of the last alert of one source, or 0 when the source has none.
SELECT COALESCE((SELECT alerted_at_ms FROM member_removal_alerts
    WHERE source_kind = @source_kind AND source_id = @source_id), 0)::bigint
    AS alerted_at_ms;

-- name: MemberRemovalTotals :one
-- The totals of the removals of one source in one window: the members that
-- the source removed from any group, the members that it removed from one
-- group, and the largest group that it saw before a removal. The window
-- holds the rows above low_ms and at most high_ms.
SELECT
    COALESCE(SUM(removed), 0)::bigint AS source_removed,
    COALESCE(SUM(CASE WHEN group_id = @group_id THEN removed ELSE 0 END), 0)::bigint AS group_removed,
    COALESCE(MAX(CASE WHEN group_id = @group_id THEN members_before ELSE 0 END), 0)::bigint AS group_members
FROM member_removals
WHERE source_kind = @source_kind AND source_id = @source_id
  AND at_ms > @low_ms::bigint AND at_ms <= @high_ms::bigint;

-- name: PruneMemberRemovals :execrows
-- Deletes at most max_rows of the oldest rows of one source at or before
-- cutoff_ms.
DELETE FROM member_removals
WHERE id IN (
    SELECT m.id FROM member_removals AS m
    WHERE m.source_kind = @source_kind AND m.source_id = @source_id
      AND m.at_ms <= @cutoff_ms::bigint
    ORDER BY m.at_ms, m.id
    LIMIT @max_rows::bigint
);

-- name: CreateRole :execrows
INSERT INTO roles (id, name, name_key, description, actions, created_at_ms, updated_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT DO NOTHING;

-- name: GetRole :one
SELECT id, name, description, actions, created_at_ms, updated_at_ms FROM roles
WHERE id = $1 AND deleted_at_ms IS NULL;

-- name: RolesByIDs :many
-- The live check is num_nulls(deleted_at_ms) = 1, not IS NULL, so no
-- partial index of the live rows can serve this lookup by ID.
-- Only NULL reads as live: a stored -1 or 0 does not, as with IS NULL.
SELECT id, name, description, actions, created_at_ms, updated_at_ms FROM roles
WHERE id = ANY(@ids::text[]) AND num_nulls(deleted_at_ms) = 1;

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
-- The live check is num_nulls(revoked_at_ms) = 1, not IS NULL, so no
-- partial index of the live rows can serve this lookup by ID.
-- Only NULL reads as live: a stored -1 or 0 does not, as with IS NULL.
SELECT id, subject, subject_kind, subject_id, role, object, target, object_kind, namespace, condition, created_at_ms, created_by
FROM grants WHERE id = $1 AND num_nulls(revoked_at_ms) = 1;

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
-- The live check is num_nulls(revoked_at_ms) = 1, not IS NULL, so no
-- partial index of the live rows can serve this lookup by ID.
-- Only NULL reads as live: a stored -1 or 0 does not, as with IS NULL.
UPDATE grants SET revoked_at_ms = $1, revoked_by = $2 WHERE id = $3 AND num_nulls(revoked_at_ms) = 1;

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

-- name: GrantsOfSubjects :many
-- The current grants of each subject of the list, in one query. The unique
-- index grants_current starts with the subject, so each subject is one seek.
SELECT id, subject, subject_kind, subject_id, role, object, target, object_kind, namespace, condition, created_at_ms, created_by
FROM grants
WHERE subject = ANY(@subjects::text[]) AND revoked_at_ms IS NULL
ORDER BY subject, target, role;

-- name: CountRoleGrantsBelowInstallation :one
SELECT COUNT(*) FROM grants
WHERE role = $1 AND object_kind IN ('namespace', 'collection', 'unit') AND revoked_at_ms IS NULL;

-- name: CountSiteAdministrators :one
-- The principals that can act now with the role $1: a current grant of the
-- role on the installation to the principal, or to a group with an active
-- membership of the principal. A principal that was ended, was disabled or
-- belongs to a namespace does not count. Only a person counts: a service
-- principal can hold the role, but every route that makes a site administrator
-- needs a fresh console session, which only a person has.
SELECT COUNT(*) FROM principals p
WHERE p.deleted_at_ms IS NULL AND p.disabled_at_ms IS NULL AND p.namespace = '' AND p.kind = 'person'
  AND EXISTS (
    SELECT 1 FROM grants g
    WHERE g.role = $1 AND g.object_kind = 'installation' AND g.revoked_at_ms IS NULL
      AND ((g.subject_kind = 'principal' AND g.subject_id = p.id)
        OR (g.subject_kind = 'group' AND EXISTS (
          SELECT 1 FROM group_members m
          JOIN principal_groups pg ON pg.id = m.group_id
          WHERE m.group_id = g.subject_id AND m.principal_id = p.id
            AND m.removed_at_ms IS NULL AND m.state = 'active' AND pg.deleted_at_ms IS NULL))));

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
  COALESCE(m.source, '')::text AS member_source,
  COALESCE(m.source_id, '')::text AS member_source_id,
  (CASE WHEN m.state = 'suspended' AND m.suspended_at_ms <= @as_of_ms::bigint
    THEN 1 ELSE 0 END)::bigint AS member_suspended,
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
  AND (m.state = 'active' OR (NOT @history::boolean AND m.state = 'suspended'))
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
    OR g.target = ANY(@inside_targets::text[])
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
  AND (m.state = 'active' OR (NOT @history::boolean AND m.state = 'suspended'))
  AND CASE WHEN @history::boolean THEN
    m.added_at_ms < @to_ms::bigint
    AND COALESCE(m.removed_at_ms, 9223372036854775807) > @from_ms::bigint
    AND m.added_at_ms < COALESCE(g.revoked_at_ms, 9223372036854775807)
    AND COALESCE(m.removed_at_ms, 9223372036854775807) > g.created_at_ms
  ELSE m.added_at_ms <= @as_of_ms::bigint AND COALESCE(m.removed_at_ms, 9223372036854775807) > @as_of_ms::bigint END
WHERE (@all_grants::boolean
    OR (@namespace::text <> '' AND g.namespace = @namespace::text AND g.object_kind <> 'installation')
    OR g.target = @target::text
    OR g.target = ANY(@inside_targets::text[])
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
  COALESCE(m.source, '')::text AS member_source,
  COALESCE(m.source_id, '')::text AS member_source_id,
  (CASE WHEN m.state = 'suspended' AND m.suspended_at_ms <= @as_of_ms::bigint
    THEN 1 ELSE 0 END)::bigint AS member_suspended,
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
-- The live check is num_nulls(p.deleted_at_ms) = 1, not IS NULL, so no
-- partial index of the live rows can serve this lookup by ID.
-- Only NULL reads as live: a stored -1 or 0 does not, as with IS NULL.
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
  ap.disable_break_glass AS policy_disable_break_glass, ap.updated_at_ms AS policy_updated_at_ms,
  ap.exchange_lifetime_ms AS policy_exchange_lifetime_ms
FROM access_tokens t
JOIN principals p ON p.id = t.principal_id
CROSS JOIN access_token_policy ap
WHERE t.digest = $1 AND num_nulls(p.deleted_at_ms) = 1 AND ap.id = 1;

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

-- name: ListCollectionUnitIDs :many
-- The stable IDs of the units in one collection, ordered. The membership of a
-- unit that was deleted stays until the collection is deleted, so these units
-- are in the list too. The collection is selected by its namespace, its name
-- and its stable ID together: a collection that was deleted and created again
-- under the same name has another ID and gives nothing.
SELECT m.unit_id
FROM collection_members AS m
JOIN collections AS c ON c.namespace = m.namespace AND c.name_key = m.collection_key
WHERE c.namespace = $1 AND c.name_key = $2 AND c.id = $3
ORDER BY m.unit_id COLLATE "C";

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

-- name: GetUnitCollectionByName :one
-- The ID and the collection of one live unit by its name, in one statement.
-- The collection is NULL when the unit is in no collection. No row means that
-- the namespace holds no live unit with the name.
SELECT i.unit_id, c.name AS collection_name
FROM inventory AS i
LEFT JOIN collection_members AS m ON m.unit_id = i.unit_id
LEFT JOIN collections AS c ON c.namespace = m.namespace AND c.name_key = m.collection_key
WHERE i.namespace = $1 AND i.tool = $2 AND i.name = $3;

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
INSERT INTO blob_purges (tool, unit_id, namespace_id, purged_at_ms)
VALUES ($1, $2, $3, $4)
ON CONFLICT (tool, unit_id) DO NOTHING;

-- name: DeleteBlobPurge :exec
DELETE FROM blob_purges WHERE tool = $1 AND unit_id = $2;

-- name: ListBlobPurges :many
-- The oldest rows first, so the sweep finishes the oldest purges first. The
-- record holds the namespace ID, so the list never joins by name.
SELECT tool, unit_id, namespace_id, purged_at_ms
FROM blob_purges
ORDER BY purged_at_ms, tool COLLATE "C", unit_id COLLATE "C"
LIMIT $1;

-- name: ListBlobPurgesAfter :many
-- The page of ListBlobPurges after the cursor (purge time, tool, unit ID).
-- The lower bound on purged_at_ms lets the index scan start at the cursor.
SELECT tool, unit_id, namespace_id, purged_at_ms
FROM blob_purges
WHERE purged_at_ms >= sqlc.arg(after_ms)
  AND (purged_at_ms > sqlc.arg(after_ms)
    OR (purged_at_ms = sqlc.arg(after_ms) AND tool COLLATE "C" > sqlc.arg(after_tool))
    OR (purged_at_ms = sqlc.arg(after_ms) AND tool = sqlc.arg(after_tool)
      AND unit_id COLLATE "C" > sqlc.arg(after_unit_id)))
ORDER BY purged_at_ms, tool COLLATE "C", unit_id COLLATE "C"
LIMIT sqlc.arg(page_limit);

-- name: CountBlobPurges :one
-- The number of blob purge records and the purge time of the oldest one. The
-- time is 0 when no record exists.
SELECT COUNT(*) AS pending, COALESCE(MIN(purged_at_ms), 0)::BIGINT AS oldest_ms
FROM blob_purges;

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
SELECT max_lifetime_ms, inactivity_ms, disable_tokens, disable_accounts, disable_break_glass, updated_at_ms,
  exchange_lifetime_ms
FROM access_token_policy WHERE id = 1;

-- name: SetAccessTokenPolicy :execrows
UPDATE access_token_policy
SET max_lifetime_ms = sqlc.arg('max_lifetime_ms')::bigint, inactivity_ms = sqlc.arg('inactivity_ms')::bigint,
  disable_tokens = sqlc.arg('disable_tokens')::boolean, disable_accounts = sqlc.arg('disable_accounts')::boolean,
  disable_break_glass = sqlc.arg('disable_break_glass')::boolean, updated_at_ms = sqlc.arg('updated_at_ms')::bigint,
  exchange_lifetime_ms = sqlc.arg('exchange_lifetime_ms')::bigint
WHERE id = 1;

-- name: DatabaseNowMs :one
-- The time of the database clock in Unix milliseconds. The sweep uses it, so
-- a wrong clock on one instance cannot disable every record.
SELECT FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint AS now_ms;

-- name: DisableInactiveAccessTokens :many
-- A token of a break-glass account is always exempt from the inactivity
-- disable. A token of a temporary administrator that still works is exempt
-- too: only the fixed expiry ends it. An expired record is a tombstone, not
-- an exemption: it never keeps a token.
UPDATE access_tokens SET disabled_at_ms = sqlc.arg('at_ms')::bigint
WHERE revoked_at_ms IS NULL AND disabled_at_ms IS NULL AND expires_at_ms > sqlc.arg('at_ms')::bigint
  AND COALESCE(last_used_at_ms, created_at_ms) <= sqlc.arg('cutoff_ms')::bigint
  AND principal_id NOT IN (SELECT principal_id FROM temp_admins WHERE expires_at_ms > sqlc.arg('at_ms')::bigint)
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
-- exempt from the inactivity disable. A temporary administrator that still
-- works is exempt too: only the fixed expiry ends it. An expired record is
-- a tombstone, not an exemption: it never keeps a principal.
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
  AND id NOT IN (SELECT principal_id FROM temp_admins WHERE expires_at_ms > sqlc.arg('at_ms')::bigint)
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
-- The session stores the session generation of its principal. A
-- generation of 0 takes the stored generation in this statement. Another
-- generation must be the stored one, so a sign-in that read its generation
-- before a password write or a reset inserts nothing.
INSERT INTO sessions (id, principal_id, digest, provider, created_at_ms, last_seen_at_ms, auth_time_ms, source_ip, user_agent, generation)
SELECT sqlc.arg('id'), p.id, sqlc.arg('digest'), sqlc.arg('provider'), sqlc.arg('created_at_ms')::bigint,
  sqlc.arg('created_at_ms')::bigint, sqlc.arg('auth_time_ms')::bigint, sqlc.arg('source_ip'), sqlc.arg('user_agent'),
  COALESCE(g.generation, 1)
FROM principals p
LEFT JOIN principal_session_generations g ON g.principal_id = p.id
WHERE p.id = sqlc.arg('principal_id') AND p.deleted_at_ms IS NULL AND p.disabled_at_ms IS NULL AND p.kind = 'person'
  AND (sqlc.arg('generation')::bigint = 0 OR COALESCE(g.generation, 1) = sqlc.arg('generation')::bigint)
ON CONFLICT DO NOTHING;

-- name: GetSessionPrincipal :one
-- The kind and the disable time of a principal that exists, so a refused
-- session insert can name its cause.
SELECT p.kind, p.disabled_at_ms, COALESCE(g.generation, 1)::bigint AS generation
FROM principals p LEFT JOIN principal_session_generations g ON g.principal_id = p.id
WHERE p.id = $1 AND p.deleted_at_ms IS NULL;

-- name: SetLastSignIn :exec
UPDATE principals SET last_sign_in_at_ms = GREATEST(COALESCE(last_sign_in_at_ms, 0), sqlc.arg('at_ms')::bigint)
WHERE id = sqlc.arg('id');

-- name: GetSession :one
SELECT id, principal_id, provider, created_at_ms, last_seen_at_ms, auth_time_ms, ended_at_ms, end_reason, source_ip, user_agent,
  generation
FROM sessions WHERE id = $1;

-- name: GetSessionByDigest :one
-- principal_active_at_ms is the latest activity of the owner by the account
-- rule of the inactivity disable, as in GetAccessTokenByDigest. The session
-- policy and the account part of the token policy come from the same read,
-- so a policy change applies to the next lookup on every instance.
-- The live check is num_nulls(p.deleted_at_ms) = 1, not IS NULL, so no
-- partial index of the live rows can serve this lookup by ID.
-- Only NULL reads as live: a stored -1 or 0 does not, as with IS NULL.
SELECT s.id, s.principal_id, s.digest, s.provider, s.created_at_ms, s.last_seen_at_ms, s.auth_time_ms, s.ended_at_ms,
  s.end_reason, s.source_ip, s.user_agent, s.generation, p.subject, p.display_name, p.kind AS principal_kind,
  p.disabled_at_ms AS principal_disabled_at_ms, p.break_glass AS principal_break_glass,
  GREATEST(p.created_at_ms, p.last_active_at_ms, COALESCE(p.last_sign_in_at_ms, 0), COALESCE((
    SELECT MAX(GREATEST(o.created_at_ms, COALESCE(o.last_used_at_ms, 0))) FROM access_tokens o WHERE o.principal_id = p.id
  ), 0), COALESCE((
    SELECT MAX(x.last_seen_at_ms) FROM sessions x WHERE x.principal_id = p.id
  ), 0))::bigint AS principal_active_at_ms,
  sp.idle_ms AS policy_idle_ms, sp.max_length_ms AS policy_max_length_ms, sp.updated_at_ms AS policy_updated_at_ms,
  sp.token_freshness_ms AS policy_token_freshness_ms,
  ap.inactivity_ms AS accounts_inactivity_ms, ap.disable_accounts AS accounts_disable,
  COALESCE(g.generation, 1)::bigint AS principal_generation
FROM sessions s
JOIN principals p ON p.id = s.principal_id
LEFT JOIN principal_session_generations g ON g.principal_id = p.id
CROSS JOIN session_policy sp
CROSS JOIN access_token_policy ap
WHERE s.digest = $1 AND num_nulls(p.deleted_at_ms) = 1 AND sp.id = 1 AND ap.id = 1;

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

-- name: DeleteEndedTicket :execrows
DELETE FROM used_tickets WHERE provider_id = $1 AND ticket_digest = $2 AND until_ms <= $3;

-- name: InsertTicket :execrows
INSERT INTO used_tickets (provider_id, ticket_digest, until_ms) VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: PruneUsedTickets :execrows
DELETE FROM used_tickets WHERE until_ms <= $1;

-- name: GetSessionPolicy :one
SELECT idle_ms, max_length_ms, updated_at_ms, token_freshness_ms FROM session_policy WHERE id = 1;

-- name: SetSessionPolicy :execrows
UPDATE session_policy
SET idle_ms = sqlc.arg('idle_ms')::bigint, max_length_ms = sqlc.arg('max_length_ms')::bigint,
  updated_at_ms = sqlc.arg('updated_at_ms')::bigint, token_freshness_ms = sqlc.narg('token_freshness_ms')::bigint
WHERE id = 1;

-- name: CreateTempAdmin :execrows
-- Records the principal as a temporary administrator with its fixed expiry.
-- A principal holds at most one record.
INSERT INTO temp_admins (principal_id, subject, expires_at_ms, created_at_ms, created_by, reason)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT DO NOTHING;

-- name: GetTempAdmin :one
SELECT principal_id, subject, expires_at_ms, created_at_ms, created_by, reason, ended_at_ms, review_opened_at_ms
FROM temp_admins WHERE principal_id = $1;

-- name: GetTempAdminBySubject :one
-- Answers the record of the live principal with the subject. A record of a
-- deleted principal never answers, so a new person with a reused subject
-- is not the old temporary administrator.
SELECT t.principal_id, t.subject, t.expires_at_ms, t.created_at_ms, t.created_by, t.reason, t.ended_at_ms, t.review_opened_at_ms
FROM temp_admins t
JOIN principals p ON p.id = t.principal_id
WHERE p.subject = $1 AND p.deleted_at_ms IS NULL;

-- name: ListTempAdmins :many
SELECT principal_id, subject, expires_at_ms, created_at_ms, created_by, reason, ended_at_ms, review_opened_at_ms
FROM temp_admins ORDER BY expires_at_ms, principal_id;

-- name: ListTempAdminsAwaitingReview :many
-- Answers the records that ended and whose review is not marked as opened.
-- The table stays small (one row for each recovery), so the query has no
-- index: a scan costs less than the upkeep of a partial index.
SELECT principal_id, subject, expires_at_ms, created_at_ms, created_by, reason, ended_at_ms, review_opened_at_ms
FROM temp_admins
WHERE ended_at_ms IS NOT NULL AND review_opened_at_ms IS NULL
ORDER BY expires_at_ms, principal_id;

-- name: MarkTempAdminReviewOpened :execrows
-- Marks the review of an ended record as opened. A record with a mark keeps
-- its first mark, and a record that did not end gets none.
UPDATE temp_admins SET review_opened_at_ms = sqlc.arg('review_opened_at_ms')::bigint
WHERE principal_id = sqlc.arg('principal_id') AND ended_at_ms IS NOT NULL AND review_opened_at_ms IS NULL;

-- name: CreateFirstHostToken :execrows
-- Inserts the first token of a temporary administrator that the host
-- command just made. It inserts nothing unless the principal is a live,
-- enabled person of the installation with no token of any status, made in
-- the window before the time of the token and not after it, and a record
-- names the principal that works past the time and the expiry of the token.
-- The live check is num_nulls(p.deleted_at_ms) = 1, not IS NULL, so no
-- partial index of the live rows can serve this lookup by ID.
-- Only NULL reads as live: a stored -1 or 0 does not, as with IS NULL.
INSERT INTO access_tokens (id, principal_id, name, kind, digest, created_at_ms, expires_at_ms, created_by)
SELECT sqlc.arg('id'), p.id, sqlc.arg('name'), sqlc.arg('kind'), sqlc.arg('digest'),
  sqlc.arg('created_at_ms')::bigint, sqlc.arg('expires_at_ms')::bigint, sqlc.arg('created_by')
FROM principals p
JOIN temp_admins t ON t.principal_id = p.id
WHERE p.id = sqlc.arg('principal_id') AND num_nulls(p.deleted_at_ms) = 1 AND p.disabled_at_ms IS NULL
  AND p.kind = 'person' AND p.namespace = ''
  AND p.created_at_ms <= sqlc.arg('created_at_ms')::bigint
  AND p.created_at_ms >= sqlc.arg('created_at_ms')::bigint - sqlc.arg('window_ms')::bigint
  AND t.expires_at_ms > sqlc.arg('created_at_ms')::bigint
  AND t.expires_at_ms >= sqlc.arg('expires_at_ms')::bigint
  AND NOT EXISTS (SELECT 1 FROM access_tokens a WHERE a.principal_id = p.id)
ON CONFLICT DO NOTHING;

-- name: DeleteTempAdmin :execrows
DELETE FROM temp_admins WHERE principal_id = $1;

-- name: RevokeTempAdminTokens :execrows
-- Ends every live token of the principal at the expiry of its temporary
-- administrator record, whatever its name. Tokens that already ended stay
-- as they are.
UPDATE access_tokens SET revoked_at_ms = $1
WHERE principal_id = $2 AND revoked_at_ms IS NULL AND disabled_at_ms IS NULL
  AND expires_at_ms > $3;

-- name: EndTempAdmin :execrows
-- Marks the record of the principal as ended at the expiry. A record that
-- already ended stays as it is, so a repeated expiry changes nothing.
UPDATE temp_admins SET ended_at_ms = $1
WHERE principal_id = $2 AND ended_at_ms IS NULL;

-- name: DisablePrincipal :execrows
-- Disables the principal. A principal that is disabled or ended stays as it
-- is. The write only narrows access. An enabled break-glass account stays
-- enabled while no other enabled break-glass account exists. The condition
-- alone does not stop two writes at the same time: under READ COMMITTED each
-- statement sees the other account as enabled, and both succeed. The caller
-- must hold the advisory lock of the server audit scope
-- (LockServerAuditScope) first. The lock serializes the writes, and the
-- statement of the second one reads the commit of the first.
UPDATE principals SET disabled_at_ms = sqlc.arg('disabled_at_ms')::bigint,
  updated_at_ms = sqlc.arg('disabled_at_ms')::bigint
WHERE principals.id = sqlc.arg('id') AND principals.deleted_at_ms IS NULL AND principals.disabled_at_ms IS NULL
  AND (NOT principals.break_glass OR EXISTS (
    SELECT 1 FROM principals o
    WHERE o.id <> principals.id AND o.break_glass AND o.kind = 'person' AND o.namespace = ''
      AND o.deleted_at_ms IS NULL AND o.disabled_at_ms IS NULL
  ));

-- name: DisablePrincipalForExpiry :execrows
-- Disables the principal at the expiry of its temporary administrator
-- record. A principal that is already disabled stays as it is.
UPDATE principals SET disabled_at_ms = sqlc.arg('disabled_at_ms')::bigint
WHERE id = sqlc.arg('id') AND deleted_at_ms IS NULL AND disabled_at_ms IS NULL;

-- name: InsertBreakGlassReview :execrows
-- Inserts the review of a new use. The unique index on (use_kind, use_ref)
-- keeps one review for each use and the primary key keeps one for each ID,
-- so a repeated open stores no second review.
INSERT INTO break_glass_reviews (
  id, use_kind, use_ref, use_scope, use_object, use_subject, use_people, use_reason, use_at_ms, use_until_ms,
  state, created_at_ms, due_at_ms, calendar, calendar_from_namespace, calendar_from_group, step, next_notice_at_ms,
  verdict_reviewer, verdict_note, verdict_auth, verdict_auth_at_ms, verdict_reviewed_at_ms, verdict_act_as, version
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23,
  $24, $25
) ON CONFLICT DO NOTHING;

-- name: GetBreakGlassReview :one
SELECT id, use_kind, use_ref, use_scope, use_object, use_subject, use_people, use_reason, use_at_ms, use_until_ms,
  state, created_at_ms, due_at_ms, calendar, calendar_from_namespace, calendar_from_group, step, next_notice_at_ms,
  verdict_reviewer, verdict_note, verdict_auth, verdict_auth_at_ms, verdict_reviewed_at_ms, verdict_act_as, version
FROM break_glass_reviews WHERE id = $1;

-- name: GetBreakGlassReviewByUse :one
-- The review of the kind and the reference of a use, for an open that runs
-- again after a crash. The unique index makes at most one row match.
SELECT id, use_kind, use_ref, use_scope, use_object, use_subject, use_people, use_reason, use_at_ms, use_until_ms,
  state, created_at_ms, due_at_ms, calendar, calendar_from_namespace, calendar_from_group, step, next_notice_at_ms,
  verdict_reviewer, verdict_note, verdict_auth, verdict_auth_at_ms, verdict_reviewed_at_ms, verdict_act_as, version
FROM break_glass_reviews WHERE use_kind = $1 AND use_ref = $2;

-- name: UpdateBreakGlassReviewVerdict :execrows
-- Stores the verdict of a review and clears the next notice. It changes no
-- row when the stored version is not the one that the caller read.
UPDATE break_glass_reviews
SET state = sqlc.arg('state'), verdict_reviewer = sqlc.arg('verdict_reviewer'),
  verdict_note = sqlc.arg('verdict_note'), verdict_auth = sqlc.arg('verdict_auth'),
  verdict_auth_at_ms = sqlc.arg('verdict_auth_at_ms'),
  verdict_reviewed_at_ms = sqlc.arg('verdict_reviewed_at_ms'), verdict_act_as = sqlc.arg('verdict_act_as'),
  next_notice_at_ms = 0, version = sqlc.arg('version')
WHERE id = sqlc.arg('id') AND version = sqlc.arg('prev_version');

-- name: UpdateBreakGlassReviewStep :execrows
-- Stores the notice step of a review. It changes no row when the stored
-- version is not the one that the caller read.
UPDATE break_glass_reviews
SET step = sqlc.arg('step'), next_notice_at_ms = sqlc.arg('next_notice_at_ms'), version = sqlc.arg('version')
WHERE id = sqlc.arg('id') AND version = sqlc.arg('prev_version');

-- name: DueBreakGlassReviews :many
-- The pending reviews with a notice due at or before now, oldest first.
SELECT id, use_kind, use_ref, use_scope, use_object, use_subject, use_people, use_reason, use_at_ms, use_until_ms,
  state, created_at_ms, due_at_ms, calendar, calendar_from_namespace, calendar_from_group, step, next_notice_at_ms,
  verdict_reviewer, verdict_note, verdict_auth, verdict_auth_at_ms, verdict_reviewed_at_ms, verdict_act_as, version
FROM break_glass_reviews
WHERE state = 'pending' AND next_notice_at_ms <> 0 AND next_notice_at_ms <= sqlc.arg('now_ms')::bigint
ORDER BY next_notice_at_ms, id
LIMIT sqlc.arg('max_rows')::bigint;

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

-- name: DisableSignInProvider :execrows
-- Disables the provider. The update applies only to the version that the
-- change read, and changes nothing but the flag, the update time and the
-- version. The write only narrows access.
UPDATE sign_in_providers
SET enabled = FALSE, updated_at_ms = sqlc.arg('updated_at_ms'), version = sqlc.arg('version') + 1
WHERE provider_id = sqlc.arg('provider_id') AND version = sqlc.arg('version');

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
-- cancel request, unless take_canceled is true. A row that another claim holds is skipped, so of several
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
    OR (o.state = 'running' AND o.claim_expires_at_ms <= c.now_ms
      AND (NOT o.cancel_requested OR sqlc.arg('take_canceled')::boolean))
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

-- name: ClaimStartedDurableOperation :one
-- Claims the pending operation id that the transaction started, for the
-- starter: the claim of a durable.StartClaim. The expiry is the database
-- clock plus the lease.
WITH c AS MATERIALIZED (
  SELECT FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint AS now_ms
)
UPDATE durable_operations o
SET state = 'running', attempt = o.attempt + 1, claim_token = o.claim_token + 1,
  claim_owner = sqlc.arg('owner')::text, claim_expires_at_ms = c.now_ms + sqlc.arg('lease_ms')::bigint,
  updated_at_ms = c.now_ms
FROM c
WHERE o.id = sqlc.arg('id')::text AND o.state = 'pending'
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

-- name: InsertDurableSchedule :execrows
INSERT INTO durable_schedules (name, interval_ms, next_run_at_ms, last_claimed_at_ms)
VALUES (sqlc.arg('name')::text, sqlc.arg('interval_ms')::bigint, sqlc.arg('now_ms')::bigint, NULL)
ON CONFLICT (name) DO NOTHING;

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

-- Durable heartbeats. Every time comes from the database clock, read in the
-- transaction of the write.

-- name: UpsertDurableHeartbeat :exec
INSERT INTO durable_heartbeats (owner, at_ms, lease_ms)
VALUES (sqlc.arg('owner')::text, sqlc.arg('now_ms')::bigint, sqlc.arg('lease_ms')::bigint)
ON CONFLICT (owner) DO UPDATE SET at_ms = excluded.at_ms, lease_ms = excluded.lease_ms;

-- name: DeleteStaleDurableHeartbeats :exec
DELETE FROM durable_heartbeats WHERE at_ms < sqlc.arg('threshold_ms')::bigint;

-- name: GetLatestDurableHeartbeat :one
SELECT owner, at_ms, lease_ms FROM durable_heartbeats
ORDER BY at_ms + 2 * lease_ms DESC, at_ms DESC, owner DESC LIMIT 1;

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

-- name: InsertImport :exec
-- A new import in the namespace with the stable ID namespace_id.
INSERT INTO imports (
    namespace_id, id, kind, address, selection, credential, profile, profile_path, options,
    state, reason, created_by, created_at_ms, plan, ended_at_ms
) VALUES (
    sqlc.arg(namespace_id), sqlc.arg(id), sqlc.arg(kind), sqlc.arg(address), sqlc.arg(selection),
    sqlc.arg(credential), sqlc.arg(profile), sqlc.arg(profile_path), sqlc.arg(options),
    sqlc.arg(state), sqlc.arg(reason), sqlc.arg(created_by), sqlc.arg(created_at_ms), sqlc.arg(plan),
    sqlc.arg(ended_at_ms)
);

-- name: GetImport :one
SELECT namespace_id, id, kind, address, selection, credential, profile, profile_path, options,
       state, reason, created_by, created_at_ms, plan, ended_at_ms
FROM imports
WHERE namespace_id = sqlc.arg(namespace_id) AND id = sqlc.arg(id);

-- name: ListImports :many
-- The imports of a namespace, oldest first.
SELECT namespace_id, id, kind, address, selection, credential, profile, profile_path, options,
       state, reason, created_by, created_at_ms, plan, ended_at_ms
FROM imports
WHERE namespace_id = sqlc.arg(namespace_id)
ORDER BY created_at_ms, id;

-- name: CountPurgedImport :one
SELECT COUNT(*)
FROM imports_purged
WHERE namespace_id = sqlc.arg(namespace_id) AND id = sqlc.arg(id);

-- name: SetImportState :execrows
-- Moves an import out of the state from_state. It changes no row when the
-- import is in another state.
UPDATE imports
SET state = sqlc.arg(state), reason = sqlc.arg(reason), ended_at_ms = sqlc.arg(ended_at_ms)
WHERE namespace_id = sqlc.arg(namespace_id) AND id = sqlc.arg(id) AND state = sqlc.arg(from_state);

-- name: SetImportPlan :exec
UPDATE imports
SET plan = sqlc.arg(plan)
WHERE namespace_id = sqlc.arg(namespace_id) AND id = sqlc.arg(id);

-- name: DeleteImport :exec
DELETE FROM imports
WHERE namespace_id = sqlc.arg(namespace_id) AND id = sqlc.arg(id);

-- name: InsertPurgedImport :exec
INSERT INTO imports_purged (namespace_id, id, purged_at_ms)
VALUES (sqlc.arg(namespace_id), sqlc.arg(id), sqlc.arg(purged_at_ms));

-- name: DeleteImportUnits :exec
DELETE FROM import_units
WHERE namespace_id = sqlc.arg(namespace_id) AND import_id = sqlc.arg(import_id);

-- name: InsertImportUnit :exec
INSERT INTO import_units (
    namespace_id, import_id, id, position, source_tool, source_name, target_name, target_tool,
    target_unit_id, state, error_code, versions, committed, bytes, encryption, findings
) VALUES (
    sqlc.arg(namespace_id), sqlc.arg(import_id), sqlc.arg(id), sqlc.arg(position), sqlc.arg(source_tool),
    sqlc.arg(source_name), sqlc.arg(target_name), sqlc.arg(target_tool), sqlc.arg(target_unit_id),
    sqlc.arg(state), sqlc.arg(error_code), sqlc.arg(versions), sqlc.arg(committed), sqlc.arg(bytes),
    sqlc.arg(encryption), sqlc.arg(findings)
);

-- name: ListImportUnits :many
-- The units of an import in the order of the plan.
SELECT namespace_id, import_id, id, position, source_tool, source_name, target_name, target_tool,
       target_unit_id, state, error_code, versions, committed, bytes, encryption, findings
FROM import_units
WHERE namespace_id = sqlc.arg(namespace_id) AND import_id = sqlc.arg(import_id)
ORDER BY position;

-- name: GetImportUnit :one
SELECT namespace_id, import_id, id, position, source_tool, source_name, target_name, target_tool,
       target_unit_id, state, error_code, versions, committed, bytes, encryption, findings
FROM import_units
WHERE namespace_id = sqlc.arg(namespace_id) AND import_id = sqlc.arg(import_id) AND id = sqlc.arg(id);

-- name: SetImportUnitState :execrows
-- Moves an import unit out of the state from_state. It changes no row when
-- the unit is in another state or does not exist.
UPDATE import_units
SET state = sqlc.arg(state), error_code = sqlc.arg(error_code)
WHERE namespace_id = sqlc.arg(namespace_id) AND import_id = sqlc.arg(import_id) AND id = sqlc.arg(id)
  AND state = sqlc.arg(from_state);

-- name: SetImportUnitCounts :execrows
UPDATE import_units
SET versions = sqlc.arg(versions), committed = sqlc.arg(committed)
WHERE namespace_id = sqlc.arg(namespace_id) AND import_id = sqlc.arg(import_id) AND id = sqlc.arg(id);

-- name: SetImportUnitTarget :execrows
-- Records the target unit of an import unit. The Tool adapters call it in the
-- transaction that creates the target.
UPDATE import_units
SET target_tool = sqlc.arg(target_tool), target_unit_id = sqlc.arg(target_unit_id)
WHERE namespace_id = sqlc.arg(namespace_id) AND import_id = sqlc.arg(import_id) AND id = sqlc.arg(id);

-- name: InsertImportLedger :exec
-- One committed source version. The Tool adapters call it in the transaction
-- of the history item.
INSERT INTO import_ledger (
    namespace_id, import_id, import_unit_id, version, source_sha256, stored_sha256, item_tool,
    item_unit_id, item_kind, item_id, committed_at_ms
) VALUES (
    sqlc.arg(namespace_id), sqlc.arg(import_id), sqlc.arg(import_unit_id), sqlc.arg(version),
    sqlc.arg(source_sha256), sqlc.arg(stored_sha256), sqlc.arg(item_tool), sqlc.arg(item_unit_id),
    sqlc.arg(item_kind), sqlc.arg(item_id), sqlc.arg(committed_at_ms)
);

-- name: CountImportLedger :one
-- The number of committed versions of an import. An import that has one
-- cannot plan again.
SELECT COUNT(*) FROM import_ledger
WHERE namespace_id = sqlc.arg(namespace_id) AND import_id = sqlc.arg(import_id);

-- name: InsertImportUploadCommit :execrows
-- The commit row of one upload version, in one statement: the namespace must
-- hold the failover version of the caller, the import must not have
-- ended, and the attempt must have started no more than deadline_ms before
-- the clock of the database, and no more than skew_ms ahead of it. The time of
-- the row is the clock of the database. It inserts no row when a check fails, and the primary key
-- refuses a second row for the version.
INSERT INTO import_upload_commits (namespace_id, import_id, unit_dir, sequence, facts, committed_at_ms)
SELECT n.id, i.id, sqlc.arg(unit_dir)::text, sqlc.arg(sequence)::bigint, sqlc.arg(facts)::text, (extract(epoch FROM clock_timestamp()) * 1000)::bigint
FROM namespaces n
JOIN imports i ON i.namespace_id = n.id
WHERE n.name = sqlc.arg(namespace) AND n.failover_version = sqlc.arg(failover_version)
  AND i.id = sqlc.arg(import_id) AND i.state NOT IN ('released', 'canceled')
  AND sqlc.arg(started_at_ms)::bigint + sqlc.arg(deadline_ms)::bigint >= (extract(epoch FROM clock_timestamp()) * 1000)::bigint
  AND sqlc.arg(started_at_ms)::bigint <= (extract(epoch FROM clock_timestamp()) * 1000)::bigint + sqlc.arg(skew_ms)::bigint;

-- name: ListImportUploadCommits :many
-- The commit rows of one unit directory of an import, in the order of the
-- sequences.
SELECT namespace_id, import_id, unit_dir, sequence, facts, committed_at_ms
FROM import_upload_commits
WHERE namespace_id = sqlc.arg(namespace_id) AND import_id = sqlc.arg(import_id)
  AND unit_dir = sqlc.arg(unit_dir)
ORDER BY sequence;

-- name: DeleteImportUploadCommits :exec
DELETE FROM import_upload_commits
WHERE namespace_id = sqlc.arg(namespace_id) AND import_id = sqlc.arg(import_id);

-- name: ListImportLedger :many
-- The ledger rows of an import unit, oldest first.
SELECT namespace_id, import_id, import_unit_id, version, source_sha256, stored_sha256, item_tool,
       item_unit_id, item_kind, item_id, committed_at_ms
FROM import_ledger
WHERE namespace_id = sqlc.arg(namespace_id) AND import_id = sqlc.arg(import_id)
  AND import_unit_id = sqlc.arg(import_unit_id)
ORDER BY committed_at_ms, version;

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

-- name: ListGateObjectsOfKind :many
-- Every object of one kind, of every scope, ordered by scope key and ID.
SELECT namespace_id, scope_key, unit_id, id, version, body
FROM gate_objects
WHERE kind = sqlc.arg(kind)
ORDER BY scope_key COLLATE "C", id COLLATE "C";

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
INSERT INTO gate_objects (namespace_id, scope_key, kind, id, level, unit_id, version, body, updated_at_ms)
VALUES (sqlc.arg(namespace_id), sqlc.arg(scope_key), sqlc.arg(kind), sqlc.arg(id), sqlc.arg(level), sqlc.arg(unit_id),
        sqlc.arg(version), sqlc.arg(body), sqlc.arg(updated_at_ms));

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
-- now, by the stable ID of the unit; for a unit, the unit. level,
-- namespace_id, scope_key and collection_key describe the scope. The
-- namespace_id and the unit_id of a row name the unit of a unit row.
SELECT o.scope_key, o.namespace_id, o.unit_id, o.version, o.body
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
              JOIN collection_members AS m ON m.namespace = n.name
              WHERE n.id = o.namespace_id AND m.unit_id = o.unit_id
                AND m.collection_key = sqlc.arg(collection_key)))
  )
ORDER BY o.scope_key COLLATE "C";

-- name: ListGateReferrers :many
-- The objects, in the scope and in the scopes after it (see
-- ListNarrowerGateObjects), that name the approval rule or the path set.
SELECT o.scope_key, o.namespace_id, o.unit_id, o.kind, o.id, o.version, o.body
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
              JOIN collection_members AS m ON m.namespace = n.name
              WHERE n.id = o.namespace_id AND m.unit_id = o.unit_id
                AND m.collection_key = sqlc.arg(collection_key)))
  )
ORDER BY o.scope_key COLLATE "C", o.kind COLLATE "C", o.id COLLATE "C";

-- name: GateUnitCollection :one
-- The key of the collection that holds the unit of a stable ID. A unit in no
-- collection has no row.
SELECT m.collection_key
FROM namespaces AS n
JOIN collection_members AS m ON m.namespace = n.name
WHERE n.id = sqlc.arg(namespace_id) AND m.unit_id = sqlc.arg(unit_id);

-- name: GetGateLiveUnit :one
-- The namespace, the tool and the name of the live unit of a stable ID. A gate
-- row of a unit names the unit by its ID only, and a check that names the
-- scope of the row reads the name here.
SELECT n.name AS namespace, i.tool, i.name
FROM namespaces AS n
JOIN inventory AS i ON i.namespace = n.name
WHERE n.id = sqlc.arg(namespace_id) AND i.unit_id = sqlc.arg(unit_id);

-- name: GetGateDeletedUnit :one
-- The namespace, the tool and the name of the deleted unit of a stable ID.
SELECT n.name AS namespace, d.tool, d.name
FROM namespaces AS n
JOIN inventory_deleted AS d ON d.namespace = n.name
WHERE n.id = sqlc.arg(namespace_id) AND d.unit_id = sqlc.arg(unit_id);

-- name: ListUnitGateObjectKeys :many
-- The kind and ID of the configuration of a unit that a purge removes, for
-- the audit entry of the purge.
SELECT kind, id
FROM gate_objects
WHERE namespace_id = sqlc.arg(namespace_id) AND level = 'unit' AND unit_id = sqlc.arg(unit_id)
ORDER BY kind, id;

-- name: ListScopeGateObjectKeys :many
-- The kind, ID and scope key of the configuration of one scope that a delete
-- removes, for the audit entry of the delete.
SELECT kind, id, scope_key
FROM gate_objects
WHERE namespace_id = sqlc.arg(namespace_id) AND scope_key = sqlc.arg(scope_key)
ORDER BY kind, id;

-- name: DeleteUnitGateObjects :exec
-- Deletes the configuration of a unit that a purge removes. The references go
-- with it.
DELETE FROM gate_objects
WHERE namespace_id = sqlc.arg(namespace_id) AND level = 'unit' AND unit_id = sqlc.arg(unit_id);

-- name: DeleteScopeGateObjects :exec
-- Deletes the configuration of one scope, for example a collection that a
-- delete removes. The references go with it.
DELETE FROM gate_objects
WHERE namespace_id = sqlc.arg(namespace_id) AND scope_key = sqlc.arg(scope_key);

-- name: InsertGateRecord :exec
INSERT INTO gate_records (id, namespace_id, point, change_kind, change_id, at_ms, last_at_ms, count, refusal_key, body)
VALUES (sqlc.arg(id), sqlc.arg(namespace_id), sqlc.arg(point), sqlc.arg(change_kind), sqlc.arg(change_id),
        sqlc.arg(at_ms), sqlc.arg(last_at_ms), sqlc.arg(count), sqlc.arg(refusal_key), sqlc.arg(body));

-- name: InsertGateRecordInNamespace :execrows
-- The insert of a record of the namespace with the name, in one statement. It
-- inserts no row when no namespace has the name or when a record has the ID.
INSERT INTO gate_records (id, namespace_id, point, change_kind, change_id, at_ms, last_at_ms, count, refusal_key, body)
SELECT sqlc.arg(id)::text, n.id, sqlc.arg(point)::text, sqlc.arg(change_kind)::text, sqlc.arg(change_id)::text,
       sqlc.arg(at_ms)::bigint, sqlc.arg(last_at_ms)::bigint, sqlc.arg(count)::bigint, sqlc.arg(refusal_key)::text,
       sqlc.arg(body)::text
FROM namespaces n
WHERE n.name = sqlc.arg(namespace)::text
  AND NOT EXISTS (SELECT 1 FROM gate_records r WHERE r.id = sqlc.arg(id)::text);

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

-- name: WorkspaceOwnedBy :one
-- The ID of a workspace of a namespace, also a deleted one that the retention
-- period keeps.
SELECT id FROM workspaces WHERE id = $1 AND namespace = $2;

-- name: AdvanceWorkspaceBlameCursor :execrows
-- Moves the blame cursor of a workspace up to an ordinal. It changes nothing,
-- and affects no row, when the cursor is already at or past the ordinal.
INSERT INTO workspace_blame_cursors (workspace_id, ordinal)
VALUES ($1, $2)
ON CONFLICT (workspace_id) DO UPDATE SET ordinal = excluded.ordinal
WHERE workspace_blame_cursors.ordinal < excluded.ordinal;

-- name: GetWorkspaceBlameCursor :one
SELECT ordinal FROM workspace_blame_cursors WHERE workspace_id = $1;

-- name: InsertWorkspaceBlameItem :exec
INSERT INTO workspace_blame_items (workspace_id, state_version_id, ordinal, opaque, lineage_changed, resource_count)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetWorkspaceBlameItem :one
SELECT ordinal, opaque, lineage_changed, resource_count
FROM workspace_blame_items
WHERE workspace_id = $1 AND state_version_id = $2;

-- name: ListWorkspaceBlameSummaries :many
-- One row for each blame item among the state versions, with the count of its
-- resource change rows by kind.
SELECT i.state_version_id, i.ordinal, i.opaque, i.lineage_changed, i.resource_count,
    COALESCE(SUM(CASE WHEN c.kind = 'created' THEN 1 ELSE 0 END), 0)::bigint AS created,
    COALESCE(SUM(CASE WHEN c.kind = 'updated' THEN 1 ELSE 0 END), 0)::bigint AS updated,
    COALESCE(SUM(CASE WHEN c.kind = 'replaced' THEN 1 ELSE 0 END), 0)::bigint AS replaced,
    COALESCE(SUM(CASE WHEN c.kind = 'deleted' THEN 1 ELSE 0 END), 0)::bigint AS deleted
FROM workspace_blame_items AS i
LEFT JOIN workspace_resource_changes AS c
    ON c.workspace_id = i.workspace_id AND c.state_version_id = i.state_version_id
WHERE i.workspace_id = sqlc.arg(workspace_id) AND i.state_version_id = ANY(sqlc.arg(state_version_ids)::text[])
GROUP BY i.state_version_id, i.ordinal, i.opaque, i.lineage_changed, i.resource_count;

-- name: InsertWorkspaceResourceChange :exec
INSERT INTO workspace_resource_changes (
    workspace_id, state_version_id, address, ordinal, kind, at_ms, by_subject, by_token_id, by_job_id,
    changed_paths, restored, forced, lineage_changed
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: ListWorkspaceResourceChangesOfItem :many
SELECT address, ordinal, kind, at_ms, by_subject, by_token_id, by_job_id, changed_paths, restored, forced,
    lineage_changed
FROM workspace_resource_changes
WHERE workspace_id = $1 AND state_version_id = $2
ORDER BY address COLLATE "C";

-- name: ListWorkspaceResourceHistory :many
-- The rows of one resource instance, newest first, with an ordinal below
-- before.
SELECT state_version_id, ordinal, kind, at_ms, by_subject, by_token_id, by_job_id, changed_paths, restored,
    forced, lineage_changed
FROM workspace_resource_changes
WHERE workspace_id = sqlc.arg(workspace_id) AND address = sqlc.arg(address) AND ordinal < sqlc.arg(before)
ORDER BY ordinal DESC
LIMIT sqlc.arg(max_rows)::bigint;

-- name: DeleteWorkspaceResourceChanges :exec
DELETE FROM workspace_resource_changes WHERE workspace_id = $1;

-- name: DeleteWorkspaceBlameItems :exec
DELETE FROM workspace_blame_items WHERE workspace_id = $1;

-- name: DeleteWorkspaceBlameCursor :exec
DELETE FROM workspace_blame_cursors WHERE workspace_id = $1;

-- Runner pools, join tokens and runners. The tables hold the stable ID of the
-- namespace. A zero in a *_ms column that can be empty means "not set".

-- name: InsertRunnerPool :exec
INSERT INTO runner_pools (id, namespace_id, name, purpose, lease_ms, concurrency, isolation, git_mode, git_helper,
                          created_at_ms, updated_at_ms)
VALUES (sqlc.arg(id), sqlc.arg(namespace_id), sqlc.arg(name), sqlc.arg(purpose), sqlc.arg(lease_ms),
        sqlc.arg(concurrency), sqlc.arg(isolation), sqlc.arg(git_mode), sqlc.arg(git_helper),
        sqlc.arg(created_at_ms), sqlc.arg(updated_at_ms));

-- name: GetRunnerPool :one
SELECT p.id, p.namespace_id, n.name AS namespace_name, p.name, p.purpose, p.lease_ms, p.concurrency, p.isolation,
       p.git_mode, p.git_helper, p.created_at_ms, p.updated_at_ms
FROM runner_pools p
JOIN namespaces n ON n.id = p.namespace_id
WHERE p.id = sqlc.arg(id);

-- name: ListRunnerPools :many
SELECT p.id, p.namespace_id, n.name AS namespace_name, p.name, p.purpose, p.lease_ms, p.concurrency, p.isolation,
       p.git_mode, p.git_helper, p.created_at_ms, p.updated_at_ms
FROM runner_pools p
JOIN namespaces n ON n.id = p.namespace_id
WHERE n.name = sqlc.arg(namespace)
ORDER BY p.name;

-- name: CountRunnerPoolsOfNamespace :one
SELECT COUNT(*) FROM runner_pools WHERE namespace_id = sqlc.arg(namespace_id);

-- name: CountRunnerPoolNamed :one
SELECT COUNT(*) FROM runner_pools WHERE namespace_id = sqlc.arg(namespace_id) AND name = sqlc.arg(name);

-- name: UpdateRunnerPool :exec
UPDATE runner_pools
SET name = sqlc.arg(name), lease_ms = sqlc.arg(lease_ms), concurrency = sqlc.arg(concurrency),
    isolation = sqlc.arg(isolation), git_mode = sqlc.arg(git_mode), git_helper = sqlc.arg(git_helper),
    updated_at_ms = sqlc.arg(updated_at_ms)
WHERE id = sqlc.arg(id);

-- name: DeleteRunnerPool :exec
DELETE FROM runner_pools WHERE id = sqlc.arg(id);

-- name: DeleteRunnerPoolJoinTokens :exec
DELETE FROM runner_join_tokens WHERE pool_id = sqlc.arg(pool_id);

-- name: DeleteRunnerPoolRunners :exec
DELETE FROM runners WHERE pool_id = sqlc.arg(pool_id);

-- name: CountOpenOperationsOfKinds :one
-- The durable operations of the kinds that did not end. A pool is in use
-- while one names it.
SELECT COUNT(*)
FROM durable_operations
WHERE kind IN (sqlc.arg(kind_a), sqlc.arg(kind_b)) AND state IN ('pending', 'running');

-- name: InsertRunnerJoinToken :exec
INSERT INTO runner_join_tokens (id, namespace_id, pool_id, sha256, created_by, created_at_ms, expires_at_ms,
                                used_at_ms, used_by, revoked_at_ms)
VALUES (sqlc.arg(id), sqlc.arg(namespace_id), sqlc.arg(pool_id), sqlc.arg(sha256), sqlc.arg(created_by),
        sqlc.arg(created_at_ms), sqlc.arg(expires_at_ms), 0, '', 0);

-- name: CountOpenRunnerJoinTokens :one
SELECT COUNT(*)
FROM runner_join_tokens
WHERE pool_id = sqlc.arg(pool_id) AND used_at_ms = 0 AND revoked_at_ms = 0 AND expires_at_ms > sqlc.arg(now_ms);

-- name: GetRunnerJoinToken :one
SELECT id, namespace_id, pool_id, sha256, created_by, created_at_ms, expires_at_ms, used_at_ms, used_by, revoked_at_ms
FROM runner_join_tokens
WHERE id = sqlc.arg(id);

-- name: GetRunnerJoinTokenBySHA256 :one
SELECT id, namespace_id, pool_id, sha256, created_by, created_at_ms, expires_at_ms, used_at_ms, used_by, revoked_at_ms
FROM runner_join_tokens
WHERE sha256 = sqlc.arg(sha256);

-- name: ListRunnerJoinTokens :many
SELECT id, namespace_id, pool_id, sha256, created_by, created_at_ms, expires_at_ms, used_at_ms, used_by, revoked_at_ms
FROM runner_join_tokens
WHERE pool_id = sqlc.arg(pool_id)
ORDER BY created_at_ms DESC, id DESC;

-- name: RevokeRunnerJoinToken :exec
UPDATE runner_join_tokens SET revoked_at_ms = sqlc.arg(revoked_at_ms) WHERE id = sqlc.arg(id);

-- name: UseRunnerJoinToken :exec
UPDATE runner_join_tokens SET used_at_ms = sqlc.arg(used_at_ms), used_by = sqlc.arg(used_by) WHERE id = sqlc.arg(id);

-- name: InsertRunner :exec
INSERT INTO runners (id, namespace_id, pool_id, name, kind, isolation, labels, release, key_public,
                     key_created_at_ms, previous_key_public, previous_key_created_at_ms, rotated_at_ms, joined_with,
                     draining, retired_at_ms, last_seen_at_ms, joined_at_ms)
VALUES (sqlc.arg(id), sqlc.arg(namespace_id), sqlc.arg(pool_id), sqlc.arg(name), sqlc.arg(kind),
        sqlc.arg(isolation), sqlc.arg(labels), sqlc.arg(release), sqlc.arg(key_public),
        sqlc.arg(key_created_at_ms), ''::bytea, 0, 0, sqlc.arg(joined_with), false, 0, sqlc.arg(last_seen_at_ms),
        sqlc.arg(joined_at_ms));

-- name: GetRunner :one
SELECT r.id, r.namespace_id, n.name AS namespace_name, r.pool_id, r.name, r.kind, r.isolation, r.labels,
       r.release, r.key_public, r.key_created_at_ms, r.previous_key_public, r.previous_key_created_at_ms,
       r.rotated_at_ms, r.joined_with, r.draining, r.retired_at_ms, r.last_seen_at_ms, r.joined_at_ms
FROM runners r
JOIN namespaces n ON n.id = r.namespace_id
WHERE r.id = sqlc.arg(id);

-- name: GetRunnerNamed :one
-- The runner of the pool with the name that is not retired.
SELECT r.id, r.namespace_id, n.name AS namespace_name, r.pool_id, r.name, r.kind, r.isolation, r.labels,
       r.release, r.key_public, r.key_created_at_ms, r.previous_key_public, r.previous_key_created_at_ms,
       r.rotated_at_ms, r.joined_with, r.draining, r.retired_at_ms, r.last_seen_at_ms, r.joined_at_ms
FROM runners r
JOIN namespaces n ON n.id = r.namespace_id
WHERE r.pool_id = sqlc.arg(pool_id) AND r.name = sqlc.arg(name) AND r.retired_at_ms = 0;

-- name: ListRunners :many
SELECT r.id, r.namespace_id, n.name AS namespace_name, r.pool_id, r.name, r.kind, r.isolation, r.labels,
       r.release, r.key_public, r.key_created_at_ms, r.previous_key_public, r.previous_key_created_at_ms,
       r.rotated_at_ms, r.joined_with, r.draining, r.retired_at_ms, r.last_seen_at_ms, r.joined_at_ms
FROM runners r
JOIN namespaces n ON n.id = r.namespace_id
WHERE r.pool_id = sqlc.arg(pool_id) AND r.retired_at_ms = 0
ORDER BY r.name;

-- name: CountRunnersOfPool :one
SELECT COUNT(*) FROM runners WHERE pool_id = sqlc.arg(pool_id) AND retired_at_ms = 0;

-- name: RejoinRunner :exec
-- A join again keeps the ID, takes the new key, and clears the previous key.
UPDATE runners
SET kind = sqlc.arg(kind), isolation = sqlc.arg(isolation), labels = sqlc.arg(labels), release = sqlc.arg(release),
    key_public = sqlc.arg(key_public), key_created_at_ms = sqlc.arg(key_created_at_ms), previous_key_public = ''::bytea,
    previous_key_created_at_ms = 0, rotated_at_ms = 0, joined_with = sqlc.arg(joined_with),
    last_seen_at_ms = sqlc.arg(last_seen_at_ms)
WHERE id = sqlc.arg(id);

-- name: TouchRunner :exec
UPDATE runners SET release = sqlc.arg(release), last_seen_at_ms = sqlc.arg(last_seen_at_ms) WHERE id = sqlc.arg(id);

-- name: RotateRunnerKey :exec
UPDATE runners
SET previous_key_public = key_public, previous_key_created_at_ms = key_created_at_ms,
    rotated_at_ms = sqlc.arg(rotated_at_ms), key_public = sqlc.arg(key_public),
    key_created_at_ms = sqlc.arg(key_created_at_ms)
WHERE id = sqlc.arg(id);

-- name: SetRunnerDraining :exec
UPDATE runners SET draining = sqlc.arg(draining)::boolean WHERE id = sqlc.arg(id);

-- name: RetireRunner :exec
UPDATE runners SET retired_at_ms = sqlc.arg(retired_at_ms) WHERE id = sqlc.arg(id);

-- name: ListRunnerClaimsWithoutSession :many
-- The operations whose live claim ExpireRunnerClaimsWithoutSession ends.
SELECT id
FROM durable_operations
WHERE state = 'running' AND claim_owner = sqlc.arg(runner_id)::text
  AND NOT EXISTS (
    SELECT 1 FROM runner_sessions s
    WHERE s.operation_id = durable_operations.id AND s.token = durable_operations.claim_token
  )
ORDER BY id;

-- name: ExpireRunnerClaimsWithoutSession :exec
-- Ends the live claims of the runner that have no job session, as an expiry
-- does: the claim has no owner and its expiry passed, so the next claim of
-- the operation takes it over with a new token.
UPDATE durable_operations
SET claim_owner = NULL, claim_expires_at_ms = 0
WHERE state = 'running' AND claim_owner = sqlc.arg(runner_id)::text
  AND NOT EXISTS (
    SELECT 1 FROM runner_sessions s
    WHERE s.operation_id = durable_operations.id AND s.token = durable_operations.claim_token
  );

-- name: ExpireRunnerClaims :exec
-- Ends every live claim of the runner, also a claim with a job session.
UPDATE durable_operations
SET claim_owner = NULL, claim_expires_at_ms = 0
WHERE state = 'running' AND claim_owner = sqlc.arg(runner_id)::text;

-- name: SetRunnerClaimExpiry :exec
UPDATE durable_operations
SET claim_expires_at_ms = sqlc.arg(expires_at_ms)
WHERE id = sqlc.arg(id) AND claim_token = sqlc.arg(token);

-- name: GetRunnerClaim :one
SELECT claimed_at_ms, job_id, phase FROM runner_claims
WHERE operation_id = sqlc.arg(operation_id) AND token = sqlc.arg(token);

-- name: InsertRunnerClaimTime :exec
INSERT INTO runner_claims (operation_id, token, claimed_at_ms, job_id, phase)
VALUES (sqlc.arg(operation_id), sqlc.arg(token), sqlc.arg(claimed_at_ms), sqlc.arg(job_id), sqlc.arg(phase))
ON CONFLICT (operation_id, token) DO NOTHING;

-- name: DeleteRunnerHandoffTokensOfClaim :exec
DELETE FROM runner_handoff_tokens WHERE operation_id = sqlc.arg(operation_id) AND token = sqlc.arg(token);

-- name: InsertRunnerHandoffToken :exec
INSERT INTO runner_handoff_tokens (sha256, operation_id, token, runner_id, issued_at_ms, expires_at_ms, used_at_ms)
VALUES (sqlc.arg(sha256), sqlc.arg(operation_id), sqlc.arg(token), sqlc.arg(runner_id), sqlc.arg(issued_at_ms),
        sqlc.arg(expires_at_ms), 0);

-- name: LockRunnerHandoffToken :one
-- Holds the row until the transaction ends, so two opens of one token run one after the other.
SELECT sha256, operation_id, token, runner_id, issued_at_ms, expires_at_ms, used_at_ms
FROM runner_handoff_tokens
WHERE sha256 = sqlc.arg(sha256)
FOR UPDATE;

-- name: UseRunnerHandoffToken :exec
UPDATE runner_handoff_tokens SET used_at_ms = sqlc.arg(used_at_ms) WHERE sha256 = sqlc.arg(sha256);

-- name: InsertRunnerSession :exec
INSERT INTO runner_sessions (id, operation_id, token, runner_id, key_public, key_created_at_ms, created_at_ms)
VALUES (sqlc.arg(id), sqlc.arg(operation_id), sqlc.arg(token), sqlc.arg(runner_id), sqlc.arg(key_public),
        sqlc.arg(key_created_at_ms), sqlc.arg(created_at_ms));

-- name: CountRunnerSessionsOfClaim :one
SELECT COUNT(*) FROM runner_sessions WHERE operation_id = sqlc.arg(operation_id) AND token = sqlc.arg(token);

-- name: GetRunnerSession :one
SELECT s.id, s.operation_id, s.token, s.runner_id, s.key_public, s.key_created_at_ms, s.created_at_ms,
       COALESCE((SELECT r.retired_at_ms FROM runners r WHERE r.id = s.runner_id), 1)::bigint AS runner_retired_at_ms
FROM runner_sessions s
WHERE s.id = sqlc.arg(id);

-- name: DeleteExpiredRunnerNonces :exec
DELETE FROM runner_nonces WHERE expires_at_ms < sqlc.arg(before_ms);

-- name: UseRunnerNonce :execrows
-- Keeps the nonce until its expiry. It changes nothing, and so returns 0
-- rows, when the store holds the nonce. The caller deletes the nonces whose
-- expiry passed first, in the same transaction.
INSERT INTO runner_nonces (subject, nonce, expires_at_ms)
VALUES (sqlc.arg(subject), sqlc.arg(nonce), sqlc.arg(expires_at_ms))
ON CONFLICT (subject, nonce) DO NOTHING;

-- name: InsertProfile :exec
-- A new live profile in the namespace with the stable ID namespace_id.
INSERT INTO profiles (
    namespace_id, id, name, latest, created_by, created_at_ms, updated_at_ms, deleted_at_ms
) VALUES (
    sqlc.arg(namespace_id), sqlc.arg(id), sqlc.arg(name), sqlc.arg(latest), sqlc.arg(created_by),
    sqlc.arg(created_at_ms), sqlc.arg(updated_at_ms), 0
);

-- name: InsertProfileRevision :exec
INSERT INTO profile_revisions (
    namespace_id, profile_id, number, document, document_sha256, author, created_at_ms, reason
) VALUES (
    sqlc.arg(namespace_id), sqlc.arg(profile_id), sqlc.arg(number), sqlc.arg(document), sqlc.arg(document_sha256),
    sqlc.arg(author), sqlc.arg(created_at_ms), sqlc.arg(reason)
);

-- name: GetLiveProfileByID :one
-- The live check is COALESCE(deleted_at_ms, -1) = 0, not deleted_at_ms = 0, so
-- no partial index of the live rows can serve this lookup by ID.
SELECT namespace_id, id, name, latest, created_by, created_at_ms, updated_at_ms, deleted_at_ms
FROM profiles
WHERE namespace_id = sqlc.arg(namespace_id) AND id = sqlc.arg(id) AND COALESCE(deleted_at_ms, -1) = 0;

-- name: GetLiveProfileByName :one
SELECT namespace_id, id, name, latest, created_by, created_at_ms, updated_at_ms, deleted_at_ms
FROM profiles
WHERE namespace_id = sqlc.arg(namespace_id) AND name = sqlc.arg(name) AND deleted_at_ms = 0;

-- name: CountLiveProfiles :one
SELECT COUNT(*)
FROM profiles
WHERE namespace_id = sqlc.arg(namespace_id) AND deleted_at_ms = 0;

-- name: ListLiveProfiles :many
-- The live profiles of a namespace after the name after_name, ordered by name.
-- A folder limits the list to the names that start with folder and a slash.
SELECT namespace_id, id, name, latest, created_by, created_at_ms, updated_at_ms, deleted_at_ms
FROM profiles
WHERE namespace_id = sqlc.arg(namespace_id)
  AND deleted_at_ms = 0
  AND name > sqlc.arg(after_name)
  AND (CAST(sqlc.arg(folder) AS TEXT) = ''
       OR substr(name, 1, length(CAST(sqlc.arg(folder) AS TEXT)) + 1) = CAST(sqlc.arg(folder) AS TEXT) || '/')
ORDER BY name
LIMIT sqlc.arg(page_limit);

-- name: GetProfileRevision :one
SELECT namespace_id, profile_id, number, document, document_sha256, author, created_at_ms, reason
FROM profile_revisions
WHERE namespace_id = sqlc.arg(namespace_id) AND profile_id = sqlc.arg(profile_id) AND number = sqlc.arg(number);

-- name: ListProfileRevisions :many
-- The revisions of a profile with a number below before_number, newest first.
SELECT namespace_id, profile_id, number, document, document_sha256, author, created_at_ms, reason
FROM profile_revisions
WHERE namespace_id = sqlc.arg(namespace_id) AND profile_id = sqlc.arg(profile_id)
  AND number < sqlc.arg(before_number)
ORDER BY number DESC
LIMIT sqlc.arg(page_limit);

-- name: MoveProfileLatest :execrows
-- Moves the newest revision from base to next. It changes no row when the
-- newest revision is another.
-- The live check is COALESCE(deleted_at_ms, -1) = 0, not deleted_at_ms = 0, so
-- no partial index of the live rows can serve this lookup by ID.
UPDATE profiles
SET latest = sqlc.arg(next), updated_at_ms = sqlc.arg(updated_at_ms)
WHERE namespace_id = sqlc.arg(namespace_id) AND id = sqlc.arg(id) AND COALESCE(deleted_at_ms, -1) = 0
  AND latest = sqlc.arg(base);

-- name: MarkProfileDeleted :execrows
-- Frees the name of a live profile. A non-zero if_latest must be the newest
-- revision.
-- The live check is COALESCE(deleted_at_ms, -1) = 0, not deleted_at_ms = 0, so
-- no partial index of the live rows can serve this lookup by ID.
UPDATE profiles
SET deleted_at_ms = sqlc.arg(deleted_at_ms)
WHERE namespace_id = sqlc.arg(namespace_id) AND id = sqlc.arg(id) AND COALESCE(deleted_at_ms, -1) = 0
  AND (CAST(sqlc.arg(if_latest) AS BIGINT) = 0 OR latest = CAST(sqlc.arg(if_latest) AS BIGINT));

-- name: InsertManualLock :execrows
-- Records the manual lock of one object. A row that exists for the object is
-- not written. The caller checks the fence in the same transaction.
INSERT INTO manual_locks (kind, object_id, namespace, holder, reason, since_ms)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (kind, object_id) DO NOTHING;

-- name: GetManualLock :one
SELECT holder, reason, since_ms
FROM manual_locks
WHERE kind = $1 AND object_id = $2;

-- name: GetManualLockForUpdate :one
-- Reads the lock and holds a row lock until the transaction ends, so that a
-- removal and a new lock cannot interleave between the read and the delete.
SELECT holder, reason, since_ms
FROM manual_locks
WHERE kind = $1 AND object_id = $2
FOR UPDATE;

-- name: DeleteManualLock :execrows
-- Deletes the lock that the caller read, and no other lock: the holder and the
-- time must match too.
DELETE FROM manual_locks
WHERE kind = $1 AND object_id = $2 AND holder = $3 AND since_ms = $4;

-- name: DeleteManualLocksOfObject :exec
-- Removes the manual lock of one object by its kind and its stable ID. The purge
-- of a unit and the delete of a collection call it in the same transaction as
-- their own rows. Defense in depth: the kind is in the filter too, so that an ID
-- of one kind never removes the lock of another kind.
DELETE FROM manual_locks
WHERE kind = $1 AND object_id = $2;

-- name: GetCoveringManualLocks :one
-- The manual locks that can cover one unit: its own lock, the lock of the
-- collection it is in now, and the lock of its namespace. No row means that the inventory holds no such unit.
SELECT ul.holder AS unit_holder, ul.reason AS unit_reason, ul.since_ms AS unit_since_ms,
       cl.holder AS collection_holder, cl.reason AS collection_reason, cl.since_ms AS collection_since_ms,
       nl.holder AS namespace_holder, nl.reason AS namespace_reason, nl.since_ms AS namespace_since_ms,
       c.name AS collection_name
FROM inventory AS i
LEFT JOIN collection_members AS m ON m.unit_id = i.unit_id
LEFT JOIN collections AS c ON c.namespace = m.namespace AND c.name_key = m.collection_key
LEFT JOIN manual_locks AS ul ON ul.kind = 'unit' AND ul.object_id = i.unit_id
LEFT JOIN manual_locks AS cl ON cl.kind = 'collection' AND cl.object_id = c.id
LEFT JOIN namespaces AS ns ON ns.name = i.namespace
LEFT JOIN manual_locks AS nl ON nl.kind = 'namespace' AND nl.object_id = ns.id
WHERE i.namespace = $1 AND i.tool = $2 AND i.name = $3;

-- name: GetCoveringManualLocksByUnitID :one
-- The same locks as GetCoveringManualLocks, found by the stable unit ID of the
-- unit in the namespace. A rename of the unit does not hide its locks. No row
-- means that the inventory holds no such unit: the caller fails closed.
SELECT i.tool AS unit_tool, i.name AS unit_name,
       ul.holder AS unit_holder, ul.reason AS unit_reason, ul.since_ms AS unit_since_ms,
       cl.holder AS collection_holder, cl.reason AS collection_reason, cl.since_ms AS collection_since_ms,
       nl.holder AS namespace_holder, nl.reason AS namespace_reason, nl.since_ms AS namespace_since_ms,
       c.name AS collection_name
FROM inventory AS i
LEFT JOIN collection_members AS m ON m.unit_id = i.unit_id
LEFT JOIN collections AS c ON c.namespace = m.namespace AND c.name_key = m.collection_key
LEFT JOIN manual_locks AS ul ON ul.kind = 'unit' AND ul.object_id = i.unit_id
LEFT JOIN manual_locks AS cl ON cl.kind = 'collection' AND cl.object_id = c.id
LEFT JOIN namespaces AS ns ON ns.name = i.namespace
LEFT JOIN manual_locks AS nl ON nl.kind = 'namespace' AND nl.object_id = ns.id
WHERE i.namespace = sqlc.arg(namespace) AND i.unit_id = sqlc.arg(unit_id);

-- name: DeleteExpiredIdempotencyRecord :exec
-- Deletes the record of the name when it is past its period: a record past
-- record_ttl_ms, or a pending record past pending_ttl_ms. The time is the
-- clock of the database. A non-zero now_override_ms replaces it, for tests.
DELETE FROM idempotency_records
WHERE principal = sqlc.arg(principal) AND method = sqlc.arg(method) AND path = sqlc.arg(path)
  AND idem_key = sqlc.arg(idem_key)
  AND (created_at_ms < (
         CASE WHEN sqlc.arg(now_override_ms)::bigint <> 0 THEN sqlc.arg(now_override_ms)::bigint
            ELSE (extract(epoch FROM clock_timestamp()) * 1000)::bigint END) - sqlc.arg(record_ttl_ms)
       OR (status = 0 AND created_at_ms < (
         CASE WHEN sqlc.arg(now_override_ms)::bigint <> 0 THEN sqlc.arg(now_override_ms)::bigint
            ELSE (extract(epoch FROM clock_timestamp()) * 1000)::bigint END) - sqlc.arg(pending_ttl_ms)));

-- name: InsertIdempotencyRecord :execrows
-- Writes a pending record. It changes no row when a record holds the name.
-- created_at_ms is the clock of the database. A non-zero now_override_ms
-- replaces it, for tests.
INSERT INTO idempotency_records (
    principal, method, path, idem_key, fingerprint, token, status, headers, body, created_at_ms
) VALUES (
    sqlc.arg(principal), sqlc.arg(method), sqlc.arg(path), sqlc.arg(idem_key), sqlc.arg(fingerprint),
    sqlc.arg(token), 0, '', ''::bytea,
    CASE WHEN sqlc.arg(now_override_ms)::bigint <> 0 THEN sqlc.arg(now_override_ms)::bigint
            ELSE (extract(epoch FROM clock_timestamp()) * 1000)::bigint END
)
ON CONFLICT (principal, method, path, idem_key) DO NOTHING;

-- name: GetIdempotencyRecord :one
SELECT fingerprint, token, status, headers, body, created_at_ms
FROM idempotency_records
WHERE principal = sqlc.arg(principal) AND method = sqlc.arg(method) AND path = sqlc.arg(path)
  AND idem_key = sqlc.arg(idem_key);

-- name: CompleteIdempotencyRecord :execrows
-- Stores the final answer of the pending record that holds token. It changes
-- no row when another request holds the record or the record is done.
UPDATE idempotency_records
SET status = sqlc.arg(status), headers = sqlc.arg(headers), body = sqlc.arg(body)
WHERE principal = sqlc.arg(principal) AND method = sqlc.arg(method) AND path = sqlc.arg(path)
  AND idem_key = sqlc.arg(idem_key) AND token = sqlc.arg(token) AND status = 0;

-- name: AbandonIdempotencyRecord :exec
DELETE FROM idempotency_records
WHERE principal = sqlc.arg(principal) AND method = sqlc.arg(method) AND path = sqlc.arg(path)
  AND idem_key = sqlc.arg(idem_key) AND token = sqlc.arg(token) AND status = 0;

-- name: SweepIdempotencyRecords :execrows
-- The time is the clock of the database. A non-zero now_override_ms replaces
-- it, for tests.
DELETE FROM idempotency_records
WHERE created_at_ms < (
         CASE WHEN sqlc.arg(now_override_ms)::bigint <> 0 THEN sqlc.arg(now_override_ms)::bigint
            ELSE (extract(epoch FROM clock_timestamp()) * 1000)::bigint END) - sqlc.arg(record_ttl_ms)
   OR (status = 0 AND created_at_ms < (
         CASE WHEN sqlc.arg(now_override_ms)::bigint <> 0 THEN sqlc.arg(now_override_ms)::bigint
            ELSE (extract(epoch FROM clock_timestamp()) * 1000)::bigint END) - sqlc.arg(pending_ttl_ms));

-- name: GetIdempotencyServerKeyShared :one
-- The share lock keeps a reset by another process from changing the row before
-- the transaction ends.
SELECT wrapped_key, wrapper_id FROM idempotency_server_key WHERE id = 1 FOR SHARE;

-- name: GetIdempotencyServerKeyExclusive :one
-- A reset takes this lock. Two processes that hold the share lock and then
-- write the row wait for each other, so a reset starts again with this lock.
SELECT wrapped_key, wrapper_id FROM idempotency_server_key WHERE id = 1 FOR UPDATE;

-- name: InsertIdempotencyServerKey :exec
-- It changes no row when a key is in place: the first writer wins.
INSERT INTO idempotency_server_key (id, wrapped_key, wrapper_id)
VALUES (1, sqlc.arg(wrapped_key), sqlc.arg(wrapper_id))
ON CONFLICT DO NOTHING;

-- name: ReplaceIdempotencyServerKey :execrows
-- It changes no row when another process replaced the key first.
UPDATE idempotency_server_key
SET wrapped_key = sqlc.arg(new_wrapped_key), wrapper_id = sqlc.arg(new_wrapper_id)
WHERE id = 1 AND wrapped_key = sqlc.arg(old_wrapped_key);

-- name: DeleteAllIdempotencyRecords :exec
DELETE FROM idempotency_records;

-- name: GetAuditChainCheck :one
-- The stored result of the scheduled audit chain check. No row means no check
-- ran.
SELECT checked_at_ms, invalid FROM audit_chain_check WHERE id = 1;

-- name: SaveAuditChainCheckResult :exec
-- Stores the count of scopes that did not match, with the time of the clock
-- of the store read in the statement.
INSERT INTO audit_chain_check (id, checked_at_ms, invalid)
VALUES (1, FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint, sqlc.arg('invalid')::bigint)
ON CONFLICT (id) DO UPDATE SET checked_at_ms = excluded.checked_at_ms, invalid = excluded.invalid;

-- name: ListAuditChainMarks :many
-- The marks of checkpoint mismatches, one for each scope.
SELECT scope, epoch, sequence, marked_at_ms FROM audit_chain_mark ORDER BY scope;

-- name: SaveAuditChainMark :exec
-- Stores the mark of a checkpoint mismatch of one scope. The first mark of a
-- scope in an epoch stays, and a mark of a newer epoch replaces the older
-- one. The time of the mark is the clock of the store, read in the statement.
INSERT INTO audit_chain_mark (scope, epoch, sequence, marked_at_ms)
VALUES (
    sqlc.arg('scope')::text, sqlc.arg('epoch')::bigint, sqlc.arg('sequence')::bigint,
    FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint
)
ON CONFLICT (scope) DO UPDATE SET epoch = excluded.epoch, sequence = excluded.sequence,
    marked_at_ms = excluded.marked_at_ms
WHERE audit_chain_mark.epoch < excluded.epoch;

-- name: NextJobSeq :one
-- The sequence number of the next job of a namespace. The write transaction
-- holds the namespace row FOR UPDATE, so two creates of one namespace never read
-- the same maximum. The unique index on (namespace_id, seq) refuses a duplicate.
SELECT (COALESCE(MAX(j.seq), 0) + 1)::bigint AS seq
FROM jobs AS j
JOIN namespaces AS n ON n.id = j.namespace_id
WHERE n.name = sqlc.arg(namespace);

-- name: InsertJob :exec
INSERT INTO jobs (
    id, namespace_id, unit_id, seq, state, wait, current_phase, current_changes, current_operation,
    idempotency_key, version, body, created_at_ms, updated_at_ms, state_changed_at_ms
) VALUES (
    sqlc.arg(id), (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)), sqlc.arg(unit_id),
    sqlc.arg(seq), sqlc.arg(state), sqlc.arg(wait), sqlc.arg(current_phase), sqlc.arg(current_changes),
    sqlc.arg(current_operation), sqlc.arg(idempotency_key), sqlc.arg(version), sqlc.arg(body),
    sqlc.arg(created_at_ms), sqlc.arg(updated_at_ms), sqlc.arg(state_changed_at_ms)
);

-- name: GetJob :one
SELECT j.id, n.name AS namespace, j.namespace_id, j.unit_id, j.seq, j.state, j.wait, j.current_phase, j.current_changes,
       j.current_operation, j.idempotency_key, j.version, j.body, j.created_at_ms, j.updated_at_ms,
       j.state_changed_at_ms
FROM jobs AS j
JOIN namespaces AS n ON n.id = j.namespace_id
WHERE j.id = sqlc.arg(id);

-- name: GetJobByKey :one
SELECT j.id, n.name AS namespace, j.namespace_id, j.unit_id, j.seq, j.state, j.wait, j.current_phase, j.current_changes,
       j.current_operation, j.idempotency_key, j.version, j.body, j.created_at_ms, j.updated_at_ms,
       j.state_changed_at_ms
FROM jobs AS j
JOIN namespaces AS n ON n.id = j.namespace_id
WHERE n.name = sqlc.arg(namespace) AND j.idempotency_key = sqlc.arg(idempotency_key);

-- name: UpdateJob :execrows
-- It changes no row when the stored version is not the one that the caller
-- read.
UPDATE jobs
SET state = sqlc.arg(state), wait = sqlc.arg(wait), current_phase = sqlc.arg(current_phase),
    current_changes = sqlc.arg(current_changes), current_operation = sqlc.arg(current_operation),
    version = sqlc.arg(version), body = sqlc.arg(body), updated_at_ms = sqlc.arg(updated_at_ms),
    state_changed_at_ms = sqlc.arg(state_changed_at_ms)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(prev_version);

-- name: CountOpenJobsOfUnit :one
SELECT COUNT(*) FROM jobs AS j
JOIN namespaces AS n ON n.id = j.namespace_id
WHERE n.name = sqlc.arg(namespace) AND j.unit_id = sqlc.arg(unit_id)
  AND j.state NOT IN ('succeeded', 'failed', 'canceled', 'rejected', 'expired');

-- name: CountOpenJobsOfNamespace :one
SELECT COUNT(*) FROM jobs AS j
JOIN namespaces AS n ON n.id = j.namespace_id
WHERE n.name = sqlc.arg(namespace)
  AND j.state NOT IN ('succeeded', 'failed', 'canceled', 'rejected', 'expired');

-- name: GetJobHolderOfUnit :one
-- A job other than the given one that holds the unit for a change.
SELECT j.id FROM jobs AS j
WHERE j.namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace))
  AND j.unit_id = sqlc.arg(unit_id) AND j.id <> sqlc.arg(except_id) AND j.current_changes
  AND j.state IN ('dispatched', 'running', 'canceling')
LIMIT 1;

-- name: ListAwaitingJobsOfUnit :many
SELECT j.id, n.name AS namespace, j.namespace_id, j.unit_id, j.seq, j.state, j.wait, j.current_phase, j.current_changes,
       j.current_operation, j.idempotency_key, j.version, j.body, j.created_at_ms, j.updated_at_ms,
       j.state_changed_at_ms
FROM jobs AS j
JOIN namespaces AS n ON n.id = j.namespace_id
WHERE j.namespace_id = (SELECT x.id FROM namespaces AS x WHERE x.name = sqlc.arg(namespace))
  AND j.unit_id = sqlc.arg(unit_id) AND j.state = 'awaiting-state'
ORDER BY j.seq;

-- name: ListOrphanJobs :many
-- Jobs that hold their unit while the durable operation of their current
-- phase does not exist or ended. op_state is NULL for a missing operation.
SELECT j.id, n.name AS namespace, j.namespace_id, j.unit_id, j.seq, j.state, j.wait, j.current_phase, j.current_changes,
       j.current_operation, j.idempotency_key, j.version, j.body, j.created_at_ms, j.updated_at_ms,
       j.state_changed_at_ms,
       d.state AS op_state
FROM jobs AS j
JOIN namespaces AS n ON n.id = j.namespace_id
LEFT JOIN durable_operations AS d ON d.id = j.current_operation
WHERE j.state IN ('dispatched', 'running', 'canceling')
  AND j.state NOT IN ('succeeded', 'failed', 'canceled', 'rejected', 'expired')
  AND (d.id IS NULL OR d.state IN ('succeeded', 'failed', 'canceled'))
ORDER BY j.seq
LIMIT sqlc.arg(max_rows);

-- name: GetActiveJobFreeze :one
SELECT f.id FROM job_freezes AS f
JOIN namespaces AS n ON n.id = f.namespace_id
WHERE n.name = sqlc.arg(namespace) AND f.starts_at_ms <= sqlc.arg(now_ms)
  AND (f.ends_at_ms = 0 OR sqlc.arg(now_ms) < f.ends_at_ms)
ORDER BY f.starts_at_ms, f.id
LIMIT 1;

-- name: InsertJobFreeze :execrows
INSERT INTO job_freezes (id, namespace_id, reason, starts_at_ms, ends_at_ms, created_by, ended_by)
VALUES (
    sqlc.arg(id), (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)), sqlc.arg(reason),
    sqlc.arg(starts_at_ms), sqlc.arg(ends_at_ms), sqlc.arg(created_by), sqlc.arg(ended_by)
)
ON CONFLICT (id) DO NOTHING;

-- name: GetJobFreeze :one
SELECT f.id, n.name AS namespace, f.reason, f.starts_at_ms, f.ends_at_ms, f.created_by, f.ended_by
FROM job_freezes AS f
JOIN namespaces AS n ON n.id = f.namespace_id
WHERE f.id = sqlc.arg(id);

-- name: EndJobFreeze :exec
UPDATE job_freezes SET ends_at_ms = sqlc.arg(ends_at_ms), ended_by = sqlc.arg(ended_by)
WHERE id = sqlc.arg(id);

-- name: ListJobFreezes :many
SELECT f.id, n.name AS namespace, f.reason, f.starts_at_ms, f.ends_at_ms, f.created_by, f.ended_by
FROM job_freezes AS f
JOIN namespaces AS n ON n.id = f.namespace_id
WHERE n.name = sqlc.arg(namespace) AND (f.ends_at_ms = 0 OR f.ends_at_ms > sqlc.arg(since_ms))
ORDER BY f.starts_at_ms, f.id;

-- name: FinalizeReleasedUnitState :one
-- Make the current state version of the unit final, with the origin of a
-- release, and do nothing when the unit has no state version row or its
-- current version is final already. It returns the version and the time that
-- the database clock gives it.
UPDATE unit_state
SET final_version_id = version_id, final_commit = sqlc.arg(final_commit), final_job = sqlc.arg(final_job),
    final_at_ms = FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint
WHERE namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace))
  AND unit_id = sqlc.arg(unit_id) AND version_id <> final_version_id
RETURNING version_id, final_at_ms;

-- name: FinalizeUnitState :one
-- Make the current state version of the unit final, with the origin of the
-- write that ends it, at the time of the database clock. A unit with no state
-- version row gets a row with the empty version as final. It returns the final
-- version and its time.
INSERT INTO unit_state (unit_id, namespace_id, version_id, final_version_id, final_commit, final_job, final_at_ms)
VALUES (
    sqlc.arg(unit_id), (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)),
    '', '', sqlc.arg(final_commit), sqlc.arg(final_job), FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint
)
ON CONFLICT (unit_id) DO UPDATE SET final_version_id = unit_state.version_id,
    final_commit = excluded.final_commit, final_job = excluded.final_job, final_at_ms = excluded.final_at_ms
RETURNING version_id, final_at_ms;

-- name: GetUnitState :one
SELECT version_id, final_version_id, final_commit, final_job, final_at_ms FROM unit_state
WHERE namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)) AND unit_id = sqlc.arg(unit_id);

-- name: PublishUnitState :exec
INSERT INTO unit_state (unit_id, namespace_id, version_id, final_version_id, final_commit, final_job, final_at_ms)
VALUES (
    sqlc.arg(unit_id), (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)),
    sqlc.arg(version_id), sqlc.arg(final_version_id), sqlc.arg(final_commit), sqlc.arg(final_job), sqlc.arg(final_at_ms)
)
ON CONFLICT (unit_id) DO UPDATE SET version_id = excluded.version_id,
    final_version_id = CASE WHEN sqlc.arg(final)::boolean THEN excluded.final_version_id ELSE unit_state.final_version_id END,
    final_commit = CASE WHEN sqlc.arg(final)::boolean THEN excluded.final_commit ELSE unit_state.final_commit END,
    final_job = CASE WHEN sqlc.arg(final)::boolean THEN excluded.final_job ELSE unit_state.final_job END,
    final_at_ms = CASE WHEN sqlc.arg(final)::boolean THEN excluded.final_at_ms ELSE unit_state.final_at_ms END;

-- name: GetPersonPreferences :one
-- One row for a live person, with the stored preferences when the person has
-- a row. No row means no live person: a service, a deleted principal or an
-- unknown ID.
SELECT p.id, pp.document, pp.updated_at_ms
FROM principals AS p
LEFT JOIN person_preferences AS pp ON pp.principal_id = p.id
WHERE p.id = sqlc.arg(id) AND p.kind = 'person' AND p.deleted_at_ms IS NULL;

-- name: LockPersonPreferences :one
-- GetPersonPreferences that locks the row of the person until the end of the
-- transaction. Two updates of one person, and the end of the person, wait for
-- each other here, so no update loses the change of another.
SELECT p.id, pp.document, pp.updated_at_ms
FROM principals AS p
LEFT JOIN person_preferences AS pp ON pp.principal_id = p.id
WHERE p.id = sqlc.arg(id) AND p.kind = 'person' AND p.deleted_at_ms IS NULL
FOR NO KEY UPDATE OF p;

-- name: UpsertPersonPreferences :exec
INSERT INTO person_preferences (principal_id, document, updated_at_ms)
VALUES (sqlc.arg(principal_id), sqlc.arg(document), sqlc.arg(updated_at_ms))
ON CONFLICT (principal_id) DO UPDATE SET document = excluded.document, updated_at_ms = excluded.updated_at_ms;

-- name: DeletePersonPreferences :exec
DELETE FROM person_preferences WHERE principal_id = sqlc.arg(principal_id);

-- name: InsertSavedView :execrows
-- The caller checks the fence in the same transaction. A view of the same
-- owner with the same name in the namespace is not written.
INSERT INTO saved_views (id, namespace_id, owner, name, query_json, created_at_ms)
VALUES (
    sqlc.arg(id), (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)),
    sqlc.arg(owner), sqlc.arg(name), sqlc.arg(query_json), sqlc.arg(created_at_ms)
)
ON CONFLICT (namespace_id, owner, name) DO NOTHING;

-- name: CountSavedViewsOfOwner :one
SELECT COUNT(*) FROM saved_views
WHERE namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)) AND owner = sqlc.arg(owner);

-- name: CountSavedViewsInNamespace :one
SELECT COUNT(*) FROM saved_views
WHERE namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace));

-- name: GetSavedView :one
-- The owner is part of the key: a view of another owner reads as no row.
SELECT v.id, n.name AS namespace, v.owner, v.name, v.query_json, v.created_at_ms
FROM saved_views AS v
JOIN namespaces AS n ON n.id = v.namespace_id
WHERE n.name = sqlc.arg(namespace) AND v.id = sqlc.arg(id) AND v.owner = sqlc.arg(owner);

-- name: ListSavedViewsOfOwner :many
SELECT v.id, n.name AS namespace, v.owner, v.name, v.query_json, v.created_at_ms
FROM saved_views AS v
JOIN namespaces AS n ON n.id = v.namespace_id
WHERE n.name = sqlc.arg(namespace) AND v.owner = sqlc.arg(owner)
-- The order is by code point, whatever the collation of the database.
ORDER BY v.name COLLATE "C", v.id COLLATE "C";

-- name: DeleteSavedView :execrows
DELETE FROM saved_views
WHERE saved_views.id = sqlc.arg(id) AND saved_views.owner = sqlc.arg(owner)
  AND saved_views.namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace));

-- name: ListSavedViewIDsOfOwner :many
SELECT v.id
FROM saved_views AS v
WHERE v.namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)) AND v.owner = sqlc.arg(owner)
ORDER BY v.id COLLATE "C";

-- name: InsertRegistryRelease :execrows
-- The caller checks the fence in the same transaction. A release of the same
-- address and version is not written.
INSERT INTO registry_releases (
    namespace_id, kind, name, system, version, sums_file, sums_sha256, signature_file, signature_sha256,
    key_fingerprint, protocols, published_by, archives_json, published_at_ms, stored_files_json
)
VALUES (
    (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)), sqlc.arg(kind), sqlc.arg(name),
    sqlc.arg(system), sqlc.arg(version), sqlc.arg(sums_file), sqlc.arg(sums_sha256), sqlc.arg(signature_file),
    sqlc.arg(signature_sha256), sqlc.arg(key_fingerprint), sqlc.arg(protocols), sqlc.arg(published_by), sqlc.arg(archives_json),
    sqlc.arg(published_at_ms), sqlc.arg(stored_files_json)
)
ON CONFLICT (namespace_id, kind, name, system, version) DO NOTHING;

-- name: GetRegistryRelease :one
SELECT n.name AS namespace, r.kind, r.name, r.system, r.version, r.sums_file, r.sums_sha256, r.signature_file,
    r.signature_sha256, r.key_fingerprint, r.protocols, r.published_by, r.archives_json, r.published_at_ms,
    r.stored_files_json
FROM registry_releases AS r
JOIN namespaces AS n ON n.id = r.namespace_id
WHERE n.name = sqlc.arg(namespace) AND r.kind = sqlc.arg(kind) AND r.name = sqlc.arg(name)
  AND r.system = sqlc.arg(system) AND r.version = sqlc.arg(version);

-- name: ListRegistryReleases :many
-- The caller orders the rows by version precedence.
SELECT n.name AS namespace, r.kind, r.name, r.system, r.version, r.sums_file, r.sums_sha256, r.signature_file,
    r.signature_sha256, r.key_fingerprint, r.protocols, r.published_by, r.archives_json, r.published_at_ms,
    r.stored_files_json
FROM registry_releases AS r
JOIN namespaces AS n ON n.id = r.namespace_id
WHERE n.name = sqlc.arg(namespace) AND r.kind = sqlc.arg(kind) AND r.name = sqlc.arg(name)
  AND r.system = sqlc.arg(system)
ORDER BY r.version COLLATE "C";

-- name: ListRegistryAddresses :many
-- One page of the distinct addresses of a kind in a namespace, after the
-- cursor (name, system). The caller reads one row more than the page size, so
-- that the extra row tells whether a next page exists.
SELECT r.name, r.system
FROM registry_releases AS r
JOIN namespaces AS n ON n.id = r.namespace_id
WHERE n.name = sqlc.arg(namespace) AND r.kind = sqlc.arg(kind)
  AND (r.name COLLATE "C" > sqlc.arg(after_name)
    OR (r.name COLLATE "C" = sqlc.arg(after_name) AND r.system COLLATE "C" > sqlc.arg(after_system)))
GROUP BY r.name, r.system
-- The order is by code point, whatever the collation of the database.
ORDER BY r.name COLLATE "C", r.system COLLATE "C"
LIMIT sqlc.arg(lim);

-- name: InsertRegistryKey :execrows
-- The caller checks the fence in the same transaction. A key with the same
-- fingerprint in the namespace is not written.
INSERT INTO registry_keys (namespace_id, fingerprint, armored_key, added_by, added_at_ms)
VALUES (
    (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)), sqlc.arg(fingerprint),
    sqlc.arg(armored_key), sqlc.arg(added_by), sqlc.arg(added_at_ms)
)
ON CONFLICT (namespace_id, fingerprint) DO NOTHING;

-- name: GetRegistryKey :one
SELECT n.name AS namespace, k.fingerprint, k.armored_key, k.added_by, k.added_at_ms
FROM registry_keys AS k
JOIN namespaces AS n ON n.id = k.namespace_id
WHERE n.name = sqlc.arg(namespace) AND k.fingerprint = sqlc.arg(fingerprint);

-- name: CountRegistryKeys :one
SELECT COUNT(*) FROM registry_keys
WHERE namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace));

-- name: ListRegistryKeys :many
SELECT n.name AS namespace, k.fingerprint, k.armored_key, k.added_by, k.added_at_ms
FROM registry_keys AS k
JOIN namespaces AS n ON n.id = k.namespace_id
WHERE n.name = sqlc.arg(namespace)
-- The order is by code point, whatever the collation of the database.
ORDER BY k.fingerprint COLLATE "C";

-- name: DeleteRegistryKey :execrows
DELETE FROM registry_keys
WHERE registry_keys.fingerprint = sqlc.arg(fingerprint)
  AND registry_keys.namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace));

-- name: GitHostNamespaceID :one
SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace);

-- name: InsertGitHostConnection :execrows
-- The caller checks the fence in the same transaction. The first connection of
-- a namespace is the default. A second connection of the namespace to the same
-- installation is not written.
INSERT INTO git_host_connections (
    namespace_id, id, kind, auth, app_scope, installation_id, push, is_default, version, created_at_ms, updated_at_ms
)
SELECT
    n.id, sqlc.arg(id), sqlc.arg(kind), sqlc.arg(auth), sqlc.arg(app_scope), sqlc.arg(installation_id),
    sqlc.arg(push), NOT EXISTS (SELECT 1 FROM git_host_connections AS c WHERE c.namespace_id = n.id), 1,
    sqlc.arg(created_at_ms), sqlc.arg(updated_at_ms)
FROM namespaces AS n
WHERE n.name = sqlc.arg(namespace)
ON CONFLICT (namespace_id, kind, installation_id) DO NOTHING;

-- name: CountGitHostConnections :one
SELECT COUNT(*) FROM git_host_connections
WHERE namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace));

-- name: GetGitHostConnection :one
SELECT n.name AS namespace, c.id, c.kind, c.auth, c.app_scope, c.installation_id, c.push, c.is_default, c.version,
    c.created_at_ms, c.updated_at_ms
FROM git_host_connections AS c
JOIN namespaces AS n ON n.id = c.namespace_id
WHERE n.name = sqlc.arg(namespace) AND c.id = sqlc.arg(id);

-- name: ListGitHostConnections :many
SELECT n.name AS namespace, c.id, c.kind, c.auth, c.app_scope, c.installation_id, c.push, c.is_default, c.version,
    c.created_at_ms, c.updated_at_ms
FROM git_host_connections AS c
JOIN namespaces AS n ON n.id = c.namespace_id
WHERE n.name = sqlc.arg(namespace)
ORDER BY c.created_at_ms, c.id COLLATE "C";

-- name: DeleteGitHostConnection :execrows
-- A version of 0 sets no condition.
DELETE FROM git_host_connections
WHERE git_host_connections.id = sqlc.arg(id)
  AND git_host_connections.namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace))
  AND (sqlc.arg(if_version)::bigint = 0 OR git_host_connections.version = sqlc.arg(if_version)::bigint);

-- name: ClearGitHostDefault :execrows
UPDATE git_host_connections
SET is_default = false, version = version + 1, updated_at_ms = GREATEST(sqlc.arg(updated_at_ms), git_host_connections.created_at_ms)
WHERE git_host_connections.namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace))
  AND git_host_connections.is_default = true AND git_host_connections.id <> sqlc.arg(id);

-- name: SetGitHostDefault :execrows
UPDATE git_host_connections
SET is_default = true, version = version + 1, updated_at_ms = GREATEST(sqlc.arg(updated_at_ms), git_host_connections.created_at_ms)
WHERE git_host_connections.namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace))
  AND git_host_connections.id = sqlc.arg(id)
  AND (sqlc.arg(if_version)::bigint = 0 OR git_host_connections.version = sqlc.arg(if_version)::bigint);

-- name: GetGitHostRegistration :one
SELECT kind, app_id, base_url, hook_id, sealed_key, key_set_id, key_created_at_ms, sealed_event_secret,
    event_secret_set_id, event_secret_created_at_ms, address_digest, version, created_at_ms, updated_at_ms
FROM git_host_app_registrations
WHERE kind = sqlc.arg(kind);

-- name: InsertGitHostRegistration :execrows
INSERT INTO git_host_app_registrations (
    kind, app_id, base_url, hook_id, sealed_key, key_set_id, key_created_at_ms, sealed_event_secret,
    event_secret_set_id, event_secret_created_at_ms, address_digest, version, created_at_ms, updated_at_ms
)
VALUES (
    sqlc.arg(kind), sqlc.arg(app_id), sqlc.arg(base_url), sqlc.arg(hook_id), sqlc.arg(sealed_key),
    sqlc.arg(key_set_id), sqlc.arg(key_created_at_ms), sqlc.arg(sealed_event_secret),
    sqlc.arg(event_secret_set_id), sqlc.arg(event_secret_created_at_ms), sqlc.arg(address_digest), 1,
    sqlc.arg(created_at_ms), sqlc.arg(updated_at_ms)
)
ON CONFLICT (kind) DO NOTHING;

-- name: UpdateGitHostRegistration :execrows
-- The hook ID and the creation time stay. A version of 0 sets no condition.
UPDATE git_host_app_registrations
SET app_id = sqlc.arg(app_id), base_url = sqlc.arg(base_url), sealed_key = sqlc.arg(sealed_key),
    key_set_id = sqlc.arg(key_set_id), key_created_at_ms = sqlc.arg(key_created_at_ms),
    sealed_event_secret = sqlc.arg(sealed_event_secret), event_secret_set_id = sqlc.arg(event_secret_set_id),
    event_secret_created_at_ms = sqlc.arg(event_secret_created_at_ms), address_digest = sqlc.arg(address_digest),
    version = version + 1, updated_at_ms = GREATEST(sqlc.arg(updated_at_ms), git_host_app_registrations.created_at_ms)
WHERE kind = sqlc.arg(kind) AND (sqlc.arg(if_version)::bigint = 0 OR version = sqlc.arg(if_version)::bigint);

-- name: DeleteGitHostRegistration :execrows
DELETE FROM git_host_app_registrations
WHERE kind = sqlc.arg(kind) AND (sqlc.arg(if_version)::bigint = 0 OR version = sqlc.arg(if_version)::bigint);

-- name: CountGitHostInstallations :one
SELECT COUNT(*) FROM git_host_installations;

-- name: GetGitHostInstallation :one
SELECT i.installation_id, n.name AS namespace, i.version, i.created_at_ms, i.updated_at_ms
FROM git_host_installations AS i
JOIN namespaces AS n ON n.id = i.namespace_id
WHERE i.installation_id = sqlc.arg(installation_id);

-- name: ListGitHostInstallations :many
SELECT i.installation_id, n.name AS namespace, i.version, i.created_at_ms, i.updated_at_ms
FROM git_host_installations AS i
JOIN namespaces AS n ON n.id = i.namespace_id
ORDER BY char_length(i.installation_id), i.installation_id COLLATE "C";

-- name: InsertGitHostInstallation :execrows
INSERT INTO git_host_installations (installation_id, namespace_id, version, created_at_ms, updated_at_ms)
SELECT sqlc.arg(installation_id), n.id, 1, sqlc.arg(created_at_ms), sqlc.arg(updated_at_ms)
FROM namespaces AS n
WHERE n.name = sqlc.arg(namespace)
ON CONFLICT (installation_id) DO NOTHING;

-- name: UpdateGitHostInstallation :execrows
-- A version of 0 sets no condition.
UPDATE git_host_installations
SET namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)),
    version = version + 1, updated_at_ms = GREATEST(sqlc.arg(updated_at_ms), git_host_installations.created_at_ms)
WHERE installation_id = sqlc.arg(installation_id) AND (sqlc.arg(if_version)::bigint = 0 OR version = sqlc.arg(if_version)::bigint);

-- name: DeleteGitHostInstallation :execrows
DELETE FROM git_host_installations
WHERE installation_id = sqlc.arg(installation_id) AND (sqlc.arg(if_version)::bigint = 0 OR version = sqlc.arg(if_version)::bigint);

-- name: CountOpenForgeLinkRequests :one
SELECT COUNT(*) AS open_requests FROM forge_link_requests
WHERE forge_link_requests.principal_id = sqlc.arg(principal_id) AND forge_link_requests.used_at_ms = 0
  AND forge_link_requests.expires_at_ms > sqlc.arg(now_ms);

-- name: DeleteExpiredForgeLinkRequestsOf :exec
DELETE FROM forge_link_requests
WHERE forge_link_requests.principal_id = sqlc.arg(principal_id) AND forge_link_requests.expires_at_ms <= sqlc.arg(before_ms);

-- name: DeleteForgeAccountLink :execrows
DELETE FROM forge_account_links
WHERE forge_account_links.kind = sqlc.arg(kind) AND forge_account_links.host = sqlc.arg(host)
  AND forge_account_links.account_id = sqlc.arg(account_id);

-- name: DeleteForgeAccountLinksOfPrincipal :exec
DELETE FROM forge_account_links WHERE forge_account_links.principal_id = sqlc.arg(principal_id);

-- name: DeleteForgeLinkRequestsOfPrincipal :exec
DELETE FROM forge_link_requests WHERE forge_link_requests.principal_id = sqlc.arg(principal_id);

-- name: GetForgeAccountLink :one
SELECT l.kind, l.host, l.account_id, l.login, l.principal_id, l.linked_at_ms
FROM forge_account_links AS l
WHERE l.kind = sqlc.arg(kind) AND l.host = sqlc.arg(host) AND l.account_id = sqlc.arg(account_id);

-- name: GetForgeLinkRequest :one
SELECT r.token_sha256, r.principal_id, r.created_at_ms, r.expires_at_ms, r.proved_kind, r.proved_host,
    r.proved_account_id, r.proved_login, r.proved_delivery_id, r.proved_at_ms, r.used_at_ms
FROM forge_link_requests AS r
WHERE r.token_sha256 = sqlc.arg(token_sha256);

-- name: InsertForgeAccountLink :exec
INSERT INTO forge_account_links (kind, host, account_id, login, principal_id, linked_at_ms)
VALUES (sqlc.arg(kind), sqlc.arg(host), sqlc.arg(account_id), sqlc.arg(login), sqlc.arg(principal_id),
    sqlc.arg(linked_at_ms));

-- name: InsertForgeLinkRequest :exec
INSERT INTO forge_link_requests (
    token_sha256, principal_id, created_at_ms, expires_at_ms, proved_kind, proved_host, proved_account_id,
    proved_login, proved_delivery_id, proved_at_ms, used_at_ms
) VALUES (
    sqlc.arg(token_sha256), sqlc.arg(principal_id), sqlc.arg(created_at_ms), sqlc.arg(expires_at_ms), '', '', '', '', '',
    0, 0
);

-- name: ListForgeAccountLinksOf :many
SELECT l.kind, l.host, l.account_id, l.login, l.principal_id, l.linked_at_ms
FROM forge_account_links AS l
WHERE l.principal_id = sqlc.arg(principal_id)
ORDER BY l.kind COLLATE "C", l.host COLLATE "C", l.account_id COLLATE "C";

-- name: ProveForgeLinkRequest :execrows
-- The first proof wins: the statement changes a row only while it has no proof
-- and no accept, and before its expiry.
UPDATE forge_link_requests
SET proved_kind = sqlc.arg(proved_kind), proved_host = sqlc.arg(proved_host),
    proved_account_id = sqlc.arg(proved_account_id), proved_login = sqlc.arg(proved_login),
    proved_delivery_id = sqlc.arg(proved_delivery_id), proved_at_ms = sqlc.arg(proved_at_ms)
WHERE forge_link_requests.token_sha256 = sqlc.arg(token_sha256) AND forge_link_requests.proved_at_ms = 0
  AND forge_link_requests.used_at_ms = 0 AND forge_link_requests.expires_at_ms > sqlc.arg(now_ms);

-- name: ReplaceForgeAccountLink :execrows
-- Replaces the link only while it names the principal that the person saw.
UPDATE forge_account_links
SET login = sqlc.arg(login), principal_id = sqlc.arg(principal_id), linked_at_ms = sqlc.arg(linked_at_ms)
WHERE forge_account_links.kind = sqlc.arg(kind) AND forge_account_links.host = sqlc.arg(host)
  AND forge_account_links.account_id = sqlc.arg(account_id)
  AND forge_account_links.principal_id = sqlc.arg(previous_principal_id);

-- name: UseForgeLinkRequest :execrows
-- The accept wins once: the statement changes a row only for the principal that
-- started it, after a proof, before its expiry, and while it has no accept.
UPDATE forge_link_requests
SET used_at_ms = sqlc.arg(used_at_ms)
WHERE forge_link_requests.token_sha256 = sqlc.arg(token_sha256) AND forge_link_requests.used_at_ms = 0
  AND forge_link_requests.proved_at_ms > 0 AND forge_link_requests.expires_at_ms > sqlc.arg(used_at_ms)
  AND forge_link_requests.principal_id = sqlc.arg(principal_id);

-- name: GetNotificationSink :one
-- The caller compares the scope of the row with the scope of the request.
SELECT id, scope AS namespace, kind, name, platform, target, created_by, version
FROM notification_sinks
WHERE id = sqlc.arg(id);

-- name: ListNotificationSinks :many
SELECT id, scope AS namespace, kind, name, platform, target, created_by, version
FROM notification_sinks
WHERE scope = sqlc.arg(namespace)
ORDER BY id COLLATE "C";

-- name: ListNotificationSinkRefs :many
-- The secret refs of every sink of a scope, in the order of each sink.
SELECT sink_id, ref
FROM notification_sink_secrets
WHERE scope = sqlc.arg(namespace)
ORDER BY sink_id COLLATE "C", position;

-- name: InsertNotificationSink :execrows
-- The caller checks the fence in the same transaction. A sink with the same
-- ID is not written.
INSERT INTO notification_sinks (id, scope, kind, name, platform, target, created_by, version)
VALUES (
    sqlc.arg(id), sqlc.arg(namespace), sqlc.arg(kind), sqlc.arg(name), sqlc.arg(platform), sqlc.arg(target),
    sqlc.arg(created_by), 1
)
ON CONFLICT (id) DO NOTHING;

-- name: UpdateNotificationSink :execrows
-- A replace changes the name and the target. The kind, the platform and the
-- creator stay. The version guards the replace.
UPDATE notification_sinks
SET name = sqlc.arg(name), target = sqlc.arg(target), version = notification_sinks.version + 1
WHERE notification_sinks.id = sqlc.arg(id) AND notification_sinks.version = sqlc.arg(version)
  AND notification_sinks.scope = sqlc.arg(namespace);

-- name: DeleteNotificationSink :execrows
DELETE FROM notification_sinks
WHERE notification_sinks.id = sqlc.arg(id) AND notification_sinks.scope = sqlc.arg(namespace);

-- name: ClaimNotificationSinkRef :execrows
-- Records that the scope holds the ref. It writes nothing, and the caller
-- refuses the new value, when the scope held the ref before.
INSERT INTO notification_sink_refs (scope, ref)
VALUES (sqlc.arg(namespace), sqlc.arg(ref))
ON CONFLICT (scope, ref) DO NOTHING;

-- name: InsertNotificationSinkSecret :exec
INSERT INTO notification_sink_secrets (scope, ref, sink_id, position, ciphertext)
VALUES (sqlc.arg(namespace), sqlc.arg(ref), sqlc.arg(sink_id), sqlc.arg(position), sqlc.arg(ciphertext));

-- name: ListNotificationSinkSecrets :many
SELECT ref, ciphertext
FROM notification_sink_secrets
WHERE scope = sqlc.arg(namespace) AND sink_id = sqlc.arg(sink_id)
ORDER BY position;

-- name: MoveNotificationSinkSecret :exec
UPDATE notification_sink_secrets
SET position = sqlc.arg(position)
WHERE notification_sink_secrets.ref = sqlc.arg(ref) AND notification_sink_secrets.sink_id = sqlc.arg(sink_id)
  AND notification_sink_secrets.scope = sqlc.arg(namespace);

-- name: DeleteNotificationSinkSecret :exec
DELETE FROM notification_sink_secrets
WHERE notification_sink_secrets.ref = sqlc.arg(ref) AND notification_sink_secrets.sink_id = sqlc.arg(sink_id)
  AND notification_sink_secrets.scope = sqlc.arg(namespace);

-- name: DeleteNotificationSinkSecrets :exec
DELETE FROM notification_sink_secrets
WHERE notification_sink_secrets.sink_id = sqlc.arg(sink_id) AND notification_sink_secrets.scope = sqlc.arg(namespace);

-- name: EnsureNotificationSequence :exec
-- Makes the counter row of a scope. A PostgreSQL adapter locks this row to
-- order the appends of the scope.
INSERT INTO notification_sequences (scope, last_sequence)
VALUES (sqlc.arg(scope), 0)
ON CONFLICT (scope) DO NOTHING;

-- name: GetNotificationSequence :one
-- Locks the counter row until the commit, so the appends of a scope wait for
-- each other and a later sequence never becomes visible before an earlier one.
SELECT last_sequence FROM notification_sequences WHERE scope = sqlc.arg(scope) FOR UPDATE;

-- name: SetNotificationSequence :exec
UPDATE notification_sequences SET last_sequence = sqlc.arg(last_sequence) WHERE scope = sqlc.arg(scope);

-- name: GetNotificationIDByDedupe :one
SELECT id FROM notifications WHERE scope = sqlc.arg(scope) AND dedupe_key = sqlc.arg(dedupe_key);

-- name: CountNotificationsByID :one
SELECT COUNT(*) FROM notifications WHERE id = sqlc.arg(id);

-- name: GetNotificationScopeByID :one
SELECT scope FROM notifications WHERE id = sqlc.arg(id);

-- name: InsertNotification :exec
INSERT INTO notifications (
    scope, sequence, id, type, object, object_id, subject, agent, severity, summary, links, attributes, recipients,
    dedupe_key, at_ms
) VALUES (
    sqlc.arg(scope), sqlc.arg(sequence), sqlc.arg(id), sqlc.arg(type), sqlc.arg(object), sqlc.arg(object_id),
    sqlc.arg(subject), sqlc.arg(agent), sqlc.arg(severity), sqlc.arg(summary), sqlc.arg(links),
    sqlc.arg(attributes), sqlc.arg(recipients), sqlc.arg(dedupe_key), sqlc.arg(at_ms)
);

-- name: ListNotificationsAfter :many
SELECT scope, sequence, id, type, object, object_id, subject, agent, severity, summary, links, attributes,
       recipients, dedupe_key, at_ms
FROM notifications
WHERE scope = sqlc.arg(scope) AND sequence > sqlc.arg(after)
ORDER BY sequence
LIMIT sqlc.arg(max_rows);

-- name: ListNotificationScopesAbove :many
-- The scopes that hold a notification above the cursor of the dispatcher.
SELECT s.scope
FROM notification_sequences AS s
LEFT JOIN notification_cursors AS c ON c.scope = s.scope AND c.name = sqlc.arg(name)
WHERE EXISTS (
    SELECT 1 FROM notifications AS n WHERE n.scope = s.scope AND n.sequence > COALESCE(c.sequence, 0)
)
ORDER BY s.scope COLLATE "C";

-- name: GetNotificationCursor :one
SELECT sequence FROM notification_cursors WHERE name = sqlc.arg(name) AND scope = sqlc.arg(scope);

-- name: EnsureNotificationCursor :exec
INSERT INTO notification_cursors (name, scope, sequence)
VALUES (sqlc.arg(name), sqlc.arg(scope), 0)
ON CONFLICT (name, scope) DO NOTHING;

-- name: MoveNotificationCursor :execrows
UPDATE notification_cursors
SET sequence = sqlc.arg(to_sequence)
WHERE name = sqlc.arg(name) AND scope = sqlc.arg(scope) AND sequence = sqlc.arg(from_sequence);

-- name: GetNotificationSubscription :one
SELECT id, scope, sink_id, types, objects, min_severity, state, reason, created_by, version
FROM notification_subscriptions
WHERE id = sqlc.arg(id);

-- name: ListNotificationSubscriptions :many
SELECT id, scope, sink_id, types, objects, min_severity, state, reason, created_by, version
FROM notification_subscriptions
WHERE scope = sqlc.arg(scope)
ORDER BY id COLLATE "C";

-- name: ListNotificationSubscriptionsOfSink :many
SELECT id, scope, sink_id, types, objects, min_severity, state, reason, created_by, version
FROM notification_subscriptions
WHERE scope = sqlc.arg(scope) AND sink_id = sqlc.arg(sink_id)
ORDER BY id COLLATE "C";

-- name: InsertNotificationSubscription :execrows
-- A subscription with the same ID is not written.
INSERT INTO notification_subscriptions (
    id, scope, sink_id, types, objects, min_severity, state, reason, created_by, version
) VALUES (
    sqlc.arg(id), sqlc.arg(scope), sqlc.arg(sink_id), sqlc.arg(types), sqlc.arg(objects), sqlc.arg(min_severity),
    sqlc.arg(state), sqlc.arg(reason), sqlc.arg(created_by), 1
)
ON CONFLICT (id) DO NOTHING;

-- name: UpdateNotificationSubscription :execrows
-- The sink, the creator and the filter of a replace are the new values. The
-- version guards the replace.
UPDATE notification_subscriptions
SET sink_id = sqlc.arg(sink_id), types = sqlc.arg(types), objects = sqlc.arg(objects),
    min_severity = sqlc.arg(min_severity), state = sqlc.arg(state), reason = sqlc.arg(reason),
    created_by = sqlc.arg(created_by), version = notification_subscriptions.version + 1
WHERE notification_subscriptions.id = sqlc.arg(id) AND notification_subscriptions.scope = sqlc.arg(scope)
  AND notification_subscriptions.version = sqlc.arg(version);

-- name: SuspendNotificationSubscription :exec
UPDATE notification_subscriptions
SET state = 'suspended', reason = sqlc.arg(reason), version = notification_subscriptions.version + 1
WHERE notification_subscriptions.id = sqlc.arg(id) AND notification_subscriptions.scope = sqlc.arg(scope);

-- name: DeleteNotificationSubscription :execrows
DELETE FROM notification_subscriptions
WHERE notification_subscriptions.id = sqlc.arg(id) AND notification_subscriptions.scope = sqlc.arg(scope);

-- name: DeletePendingNotificationDeliveriesOfSubscription :exec
DELETE FROM notification_deliveries
WHERE scope = sqlc.arg(scope) AND subscription_id = sqlc.arg(subscription_id) AND state = 'pending';

-- name: SkipPendingNotificationDeliveriesOfSubscription :exec
UPDATE notification_deliveries
SET state = 'skipped', owner = '', lease_until_ms = 0, version = notification_deliveries.version + 1
WHERE scope = sqlc.arg(scope) AND subscription_id = sqlc.arg(subscription_id) AND state = 'pending';

-- name: GetNotificationDelivery :one
SELECT id, scope, notification_id, subscription_id, sink_id, contact, principal, state, attempts, next_at_ms,
       last_error, delivered_at_ms, owner, lease_until_ms, version
FROM notification_deliveries
WHERE id = sqlc.arg(id);

-- name: CountNotificationDeliveryRoute :one
SELECT COUNT(*) FROM notification_deliveries
WHERE scope = sqlc.arg(scope) AND notification_id = sqlc.arg(notification_id)
  AND subscription_id = sqlc.arg(subscription_id) AND contact = sqlc.arg(contact);

-- name: InsertNotificationDelivery :exec
INSERT INTO notification_deliveries (
    id, scope, notification_id, subscription_id, sink_id, contact, principal, state, attempts, next_at_ms,
    last_error, delivered_at_ms, owner, lease_until_ms, version
) VALUES (
    sqlc.arg(id), sqlc.arg(scope), sqlc.arg(notification_id), sqlc.arg(subscription_id), sqlc.arg(sink_id),
    sqlc.arg(contact), sqlc.arg(principal), 'pending', 0, sqlc.arg(next_at_ms), '', 0, '', 0, 1
);

-- name: ListDueNotificationDeliveryScopes :many
SELECT scope
FROM notification_deliveries
WHERE state = 'pending' AND next_at_ms <= sqlc.arg(now_ms) AND lease_until_ms <= sqlc.arg(now_ms)
GROUP BY scope
ORDER BY scope COLLATE "C";

-- name: ClaimNotificationDeliveries :many
-- Takes at most max_rows pending deliveries of the scope that are due and that
-- no live claim holds, oldest first. The caller runs it in a write transaction.
UPDATE notification_deliveries
SET owner = sqlc.arg(owner), lease_until_ms = sqlc.arg(lease_until_ms), version = notification_deliveries.version + 1
WHERE notification_deliveries.id IN (
    SELECT d.id FROM notification_deliveries AS d
    WHERE d.scope = sqlc.arg(scope) AND d.state = 'pending' AND d.next_at_ms <= sqlc.arg(now_ms)
      AND d.lease_until_ms <= sqlc.arg(now_ms)
    ORDER BY d.next_at_ms, d.id COLLATE "C"
    LIMIT sqlc.arg(max_rows)
    FOR UPDATE SKIP LOCKED
)
RETURNING id, scope, notification_id, subscription_id, sink_id, contact, principal, state, attempts, next_at_ms,
          last_error, delivered_at_ms, owner, lease_until_ms, version;

-- name: RecordNotificationAttempt :execrows
-- Stores the result for the live claim only: the owner and the version of the
-- stored claim, and a lease that has not passed at now_ms.
UPDATE notification_deliveries
SET state = sqlc.arg(state), attempts = sqlc.arg(attempts), next_at_ms = sqlc.arg(next_at_ms),
    last_error = sqlc.arg(last_error), delivered_at_ms = sqlc.arg(delivered_at_ms), owner = '',
    lease_until_ms = 0, version = notification_deliveries.version + 1
WHERE notification_deliveries.id = sqlc.arg(id) AND notification_deliveries.scope = sqlc.arg(scope)
  AND notification_deliveries.owner = sqlc.arg(owner) AND notification_deliveries.version = sqlc.arg(version)
  AND notification_deliveries.state = 'pending' AND notification_deliveries.lease_until_ms > sqlc.arg(now_ms);

-- name: CountNotificationDeliveriesOfSubscriptionBefore :one
-- The number of deliveries of the subscription at or before the cursor (next
-- time, delivery ID) in the order of ListNotificationDeliveriesOfSubscription:
-- the deliveries that a page after the cursor leaves behind, in byte order
-- (COLLATE "C"). The count stops at max_rows. The ORDER BY does not change
-- the count; it stays so that the plan reads the index of the history in
-- order and stops at max_rows.
SELECT COUNT(*) FROM (
    SELECT 1 FROM notification_deliveries
    WHERE scope = sqlc.arg(scope) AND subscription_id = sqlc.arg(subscription_id)
      AND (next_at_ms > sqlc.arg(after_ms)
        OR (next_at_ms = sqlc.arg(after_ms) AND id COLLATE "C" >= sqlc.arg(after_id)))
    ORDER BY next_at_ms DESC, id COLLATE "C" DESC
    LIMIT sqlc.arg(max_rows)
) AS within_bound;

-- name: ListNotificationDeliveriesOfSubscription :many
SELECT id, scope, notification_id, subscription_id, sink_id, contact, principal, state, attempts, next_at_ms,
       last_error, delivered_at_ms, owner, lease_until_ms, version
FROM notification_deliveries
WHERE scope = sqlc.arg(scope) AND subscription_id = sqlc.arg(subscription_id)
ORDER BY next_at_ms DESC, id COLLATE "C" DESC
LIMIT sqlc.arg(max_rows);

-- name: ListNotificationDeliveriesOfSubscriptionAfter :many
-- The page of ListNotificationDeliveriesOfSubscription after the cursor (next
-- time, delivery ID): the rows with an older next time, or with the same next
-- time and a smaller ID, in byte order (COLLATE "C"), as the order of the
-- query. The bound on next_at_ms lets the index scan start at the cursor.
SELECT id, scope, notification_id, subscription_id, sink_id, contact, principal, state, attempts, next_at_ms,
       last_error, delivered_at_ms, owner, lease_until_ms, version
FROM notification_deliveries
WHERE scope = sqlc.arg(scope) AND subscription_id = sqlc.arg(subscription_id)
  AND (next_at_ms < sqlc.arg(after_ms)
    OR (next_at_ms = sqlc.arg(after_ms) AND id COLLATE "C" < sqlc.arg(after_id)))
ORDER BY next_at_ms DESC, id COLLATE "C" DESC
LIMIT sqlc.arg(max_rows);

-- name: RedeliverNotificationDelivery :execrows
UPDATE notification_deliveries
SET state = 'pending', attempts = 0, last_error = '', owner = '', lease_until_ms = 0,
    version = notification_deliveries.version + 1
WHERE notification_deliveries.id = sqlc.arg(id) AND notification_deliveries.scope = sqlc.arg(scope)
  AND notification_deliveries.state <> 'pending';

-- name: UpsertTrigger :execrows
-- The caller checks the fence in the same transaction. A row that exists keeps
-- its unit, its kind and its creation time.
INSERT INTO triggers (
    namespace_id, id, unit_tool, unit_id, kind, preset, cron, source_namespace, source_tool, source_unit_id, enabled,
    last_saver_subject, last_saver_id, created_at_ms, updated_at_ms, next_slot_ms
)
SELECT
    n.id, sqlc.arg(id), sqlc.arg(unit_tool), sqlc.arg(unit_id), sqlc.arg(kind), sqlc.arg(preset), sqlc.arg(cron),
    sqlc.arg(source_namespace), sqlc.arg(source_tool), sqlc.arg(source_unit_id), sqlc.arg(enabled)::bigint,
    sqlc.arg(last_saver_subject), sqlc.arg(last_saver_id), sqlc.arg(created_at_ms)::bigint, sqlc.arg(updated_at_ms)::bigint,
    sqlc.arg(next_slot_ms)::bigint
FROM namespaces AS n
WHERE n.name = sqlc.arg(namespace)
ON CONFLICT (namespace_id, id) DO UPDATE SET
    preset = excluded.preset, cron = excluded.cron, source_namespace = excluded.source_namespace,
    source_tool = excluded.source_tool, source_unit_id = excluded.source_unit_id, enabled = excluded.enabled,
    last_saver_subject = excluded.last_saver_subject, last_saver_id = excluded.last_saver_id,
    updated_at_ms = GREATEST(triggers.updated_at_ms, excluded.updated_at_ms), next_slot_ms = excluded.next_slot_ms;

-- name: GetTrigger :one
SELECT n.name AS namespace, t.id, t.unit_tool, t.unit_id, t.kind, t.preset, t.cron, t.source_namespace,
    t.source_tool, t.source_unit_id, t.enabled, t.last_saver_subject, t.last_saver_id, t.created_at_ms, t.updated_at_ms,
    t.next_slot_ms
FROM triggers AS t
JOIN namespaces AS n ON n.id = t.namespace_id
WHERE n.name = sqlc.arg(namespace) AND t.id = sqlc.arg(id);

-- name: ListTriggersOfUnit :many
SELECT n.name AS namespace, t.id, t.unit_tool, t.unit_id, t.kind, t.preset, t.cron, t.source_namespace,
    t.source_tool, t.source_unit_id, t.enabled, t.last_saver_subject, t.last_saver_id, t.created_at_ms, t.updated_at_ms,
    t.next_slot_ms
FROM triggers AS t
JOIN namespaces AS n ON n.id = t.namespace_id
WHERE n.name = sqlc.arg(namespace) AND t.unit_tool = sqlc.arg(unit_tool) AND t.unit_id = sqlc.arg(unit_id)
ORDER BY t.id COLLATE "C";

-- name: ListTriggerFollowers :many
SELECT n.name AS namespace, t.id, t.unit_tool, t.unit_id, t.kind, t.preset, t.cron, t.source_namespace,
    t.source_tool, t.source_unit_id, t.enabled, t.last_saver_subject, t.last_saver_id, t.created_at_ms, t.updated_at_ms,
    t.next_slot_ms
FROM triggers AS t
JOIN namespaces AS n ON n.id = t.namespace_id
WHERE n.name = sqlc.arg(namespace) AND t.kind = 'follows' AND t.enabled = 1
  AND t.source_namespace = sqlc.arg(source_namespace) AND t.source_tool = sqlc.arg(source_tool)
  AND t.source_unit_id = sqlc.arg(source_unit_id)
ORDER BY t.id COLLATE "C";

-- name: ListEnabledFollowTriggers :many
SELECT n.name AS namespace, t.id, t.unit_tool, t.unit_id, t.kind, t.preset, t.cron, t.source_namespace,
    t.source_tool, t.source_unit_id, t.enabled, t.last_saver_subject, t.last_saver_id, t.created_at_ms, t.updated_at_ms,
    t.next_slot_ms
FROM triggers AS t
JOIN namespaces AS n ON n.id = t.namespace_id
WHERE n.name = sqlc.arg(namespace) AND t.kind = 'follows' AND t.enabled = 1
ORDER BY t.id COLLATE "C";

-- name: MoveTriggerNextSlot :execrows
-- Moves the next slot of a schedule trigger from the value that the caller read
-- to the value after the claim. The compare on the old value is a layer of its
-- own: the transaction of the caller already serializes the writes of the
-- namespace, and this statement still changes no row when the slot moved.
UPDATE triggers
SET next_slot_ms = sqlc.arg(next_slot_ms)::bigint
WHERE triggers.id = sqlc.arg(id)
  AND triggers.kind = 'schedule' AND triggers.enabled = 1
  AND triggers.next_slot_ms = sqlc.arg(old_next_slot_ms)::bigint
  AND triggers.namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace));

-- name: DeleteTrigger :execrows
DELETE FROM triggers
WHERE triggers.id = sqlc.arg(id)
  AND triggers.namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace));

-- name: GetDriftResult :one
-- The namespace and the tool are part of the key: a result of another
-- namespace or another tool reads as no row.
SELECT d.unit_id, n.name AS namespace, d.tool, d.job_id, d.status, d.checked_at_ms, d.truncated, d.resources_json
FROM drift_results AS d
JOIN namespaces AS n ON n.id = d.namespace_id
WHERE n.name = sqlc.arg(namespace) AND d.unit_id = sqlc.arg(unit_id) AND d.tool = sqlc.arg(tool);

-- name: UpsertDriftResult :execrows
-- The caller checks the fence, the job and the time in the same transaction.
-- A row of the same unit ID in another namespace or of another tool is not
-- replaced: the count is 0. A stored row of the same job is not replaced
-- either (count 0). A stored row that is not older is not replaced either
-- (count 0), so that two writers cannot put an older result over a newer one.
INSERT INTO drift_results (unit_id, namespace_id, tool, job_id, status, checked_at_ms, truncated, resources_json)
VALUES (
    sqlc.arg(unit_id), (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)), sqlc.arg(tool),
    sqlc.arg(job_id), sqlc.arg(status), sqlc.arg(checked_at_ms), sqlc.arg(truncated), sqlc.arg(resources_json)
)
ON CONFLICT (unit_id) DO UPDATE SET
    job_id = excluded.job_id, status = excluded.status, checked_at_ms = excluded.checked_at_ms,
    truncated = excluded.truncated, resources_json = excluded.resources_json
WHERE drift_results.namespace_id = excluded.namespace_id AND drift_results.tool = excluded.tool
  AND drift_results.job_id <> excluded.job_id
  AND drift_results.checked_at_ms < excluded.checked_at_ms;

-- name: DeleteDriftResult :exec
DELETE FROM drift_results
WHERE drift_results.unit_id = sqlc.arg(unit_id)
  AND drift_results.namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace));

-- name: InsertItemKey :execrows
-- The caller checks the fence in the same transaction. A record for the same
-- item is not written: the caller reads the stored record.
INSERT INTO item_keys (
    id, namespace_id, scope_id, kind, item, wrapper_id, created_at_ms, shredded_at_ms, blob_version, blob_size, blob_md5
)
VALUES (
    sqlc.arg(id), (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)),
    sqlc.arg(scope_id), sqlc.arg(kind), sqlc.arg(item), sqlc.arg(wrapper_id), sqlc.arg(created_at_ms), 0,
    sqlc.arg(blob_version), sqlc.arg(blob_size), sqlc.arg(blob_md5)
)
ON CONFLICT (namespace_id, scope_id, kind, item) DO NOTHING;

-- name: GetItemKey :one
SELECT k.id, n.name AS namespace, k.scope_id, k.kind, k.item, k.wrapper_id, k.created_at_ms, k.shredded_at_ms,
       k.blob_version, k.blob_size, k.blob_md5
FROM item_keys AS k
JOIN namespaces AS n ON n.id = k.namespace_id
WHERE n.name = sqlc.arg(namespace) AND k.scope_id = sqlc.arg(scope_id) AND k.kind = sqlc.arg(kind)
  AND k.item = sqlc.arg(item);

-- name: ShredItemKey :execrows
-- Only a record that is not shredded changes, so a second shred keeps the
-- first shred time.
UPDATE item_keys
SET shredded_at_ms = sqlc.arg(shredded_at_ms)
WHERE item_keys.shredded_at_ms = 0 AND item_keys.scope_id = sqlc.arg(scope_id) AND item_keys.kind = sqlc.arg(kind)
  AND item_keys.item = sqlc.arg(item)
  AND item_keys.namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace));

-- name: ListItemKeys :many
SELECT k.id, n.name AS namespace, k.scope_id, k.kind, k.item, k.wrapper_id, k.created_at_ms, k.shredded_at_ms,
       k.blob_version, k.blob_size, k.blob_md5
FROM item_keys AS k
JOIN namespaces AS n ON n.id = k.namespace_id
WHERE n.name = sqlc.arg(namespace) AND k.scope_id = sqlc.arg(scope_id) AND k.id > sqlc.arg(after)
ORDER BY k.id
LIMIT sqlc.arg(max_rows)::bigint;

-- name: ListItemKeyWrapperIDs :many
SELECT DISTINCT wrapper_id
FROM item_keys
WHERE shredded_at_ms = 0
ORDER BY wrapper_id;

-- name: InsertJobPlan :exec
-- The caller checks the fence and the claim in the same transaction.
INSERT INTO job_plans (id, namespace_id, job_id, body, created_at_ms)
VALUES (
    sqlc.arg(id), (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)), sqlc.arg(job_id),
    sqlc.arg(body), sqlc.arg(created_at_ms)
);

-- name: UpdateJobPlanBody :exec
-- The caller checks the fence and the claim in the same transaction.
UPDATE job_plans SET body = sqlc.arg(body)
WHERE job_plans.id = sqlc.arg(id) AND job_plans.namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace));

-- name: GetJobPlan :one
SELECT p.id, p.job_id, p.body, p.created_at_ms
FROM job_plans AS p
JOIN namespaces AS n ON n.id = p.namespace_id
WHERE n.name = sqlc.arg(namespace) AND p.id = sqlc.arg(id);

-- name: InsertJobLog :exec
-- The caller checks the fence and the claim in the same transaction.
INSERT INTO job_logs (id, namespace_id, job_id, phase, step, token, created_at_ms)
VALUES (
    sqlc.arg(id), (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)), sqlc.arg(job_id),
    sqlc.arg(phase), sqlc.arg(step), sqlc.arg(token), sqlc.arg(created_at_ms)
);

-- name: GetJobLog :one
SELECT l.id, l.job_id, l.phase, l.step, l.token, l.created_at_ms
FROM job_logs AS l
JOIN namespaces AS n ON n.id = l.namespace_id
WHERE n.name = sqlc.arg(namespace) AND l.id = sqlc.arg(id);

-- name: InsertJobLogChunk :exec
-- The write transaction of the namespace serializes the reserves of a chunk,
-- so the caller reads the row before it inserts. A row that exists stays.
INSERT INTO job_log_chunks (log_id, seq, namespace_id, sha256, size, confirmed)
VALUES (
    sqlc.arg(log_id), sqlc.arg(seq), (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)),
    sqlc.arg(sha256), sqlc.arg(size), 0
)
ON CONFLICT (log_id, seq) DO NOTHING;

-- name: GetJobLogChunk :one
SELECT c.sha256, c.size, c.confirmed, c.blob_key
FROM job_log_chunks AS c
JOIN namespaces AS n ON n.id = c.namespace_id
WHERE n.name = sqlc.arg(namespace) AND c.log_id = sqlc.arg(log_id) AND c.seq = sqlc.arg(seq);

-- name: ConfirmJobLogChunk :execrows
UPDATE job_log_chunks
SET confirmed = 1, stored_size = sqlc.arg(stored_size), stored_md5 = sqlc.arg(stored_md5),
    blob_key = sqlc.arg(blob_key)
WHERE job_log_chunks.log_id = sqlc.arg(log_id) AND job_log_chunks.seq = sqlc.arg(seq)
  AND job_log_chunks.namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace));

-- name: ListJobLogChunkKeys :many
-- The blob key of each confirmed chunk of one log version, in the order of the
-- chunk numbers.
SELECT c.seq, c.blob_key
FROM job_log_chunks AS c
JOIN namespaces AS n ON n.id = c.namespace_id
WHERE n.name = sqlc.arg(namespace) AND c.log_id = sqlc.arg(log_id) AND c.confirmed = 1
ORDER BY c.seq;

-- name: ListJobPlanIDs :many
-- The ID of each plan row of one job: a stored plan, a plan that a replan
-- cleared and a reserved plan. The sweep of the artifact blobs keeps every
-- part of each of these plans.
SELECT p.id
FROM job_plans AS p
JOIN namespaces AS n ON n.id = p.namespace_id
WHERE n.name = sqlc.arg(namespace) AND p.job_id = sqlc.arg(job_id)
ORDER BY p.id;

-- name: ListJobChunkBlobKeys :many
-- The blob key of each confirmed chunk of each log version of one job. The
-- sweep of the artifact blobs keeps these blobs.
SELECT c.blob_key
FROM job_log_chunks AS c
JOIN job_logs AS l ON l.id = c.log_id AND l.namespace_id = c.namespace_id
JOIN namespaces AS n ON n.id = c.namespace_id
WHERE n.name = sqlc.arg(namespace) AND l.job_id = sqlc.arg(job_id) AND c.confirmed = 1
ORDER BY c.blob_key;

-- name: UpsertInstanceVersion :exec
-- Writes the version row of one server process. A row with the same ID is
-- replaced, so a process that starts again with its ID keeps one row.
INSERT INTO instance_versions (id, binary_version, started_at_ms)
VALUES (sqlc.arg(id), sqlc.arg(binary_version), sqlc.arg(started_at_ms))
ON CONFLICT (id) DO UPDATE
SET binary_version = excluded.binary_version, started_at_ms = excluded.started_at_ms;

-- name: DeleteInstanceVersion :execrows
-- Deletes the version row of one server process. It deletes nothing when no
-- row has the ID.
DELETE FROM instance_versions
WHERE id = sqlc.arg(id);

-- name: ListInstanceVersions :many
-- Lists every version row, oldest start first.
SELECT id, binary_version, started_at_ms
FROM instance_versions
ORDER BY started_at_ms, id;

-- name: ListSettings :many
-- The database values of one scope, in key order.
SELECT key, value
FROM settings
WHERE scope_id = sqlc.arg(scope_id)
ORDER BY key;

-- name: GetSettingsState :one
-- The revision and the digest of the static layer of one scope. No row means
-- a scope that never changed.
SELECT revision, static_digest
FROM settings_state
WHERE scope_id = sqlc.arg(scope_id);

-- name: UpsertSetting :exec
INSERT INTO settings (scope_id, key, value, updated_at_ms)
VALUES (sqlc.arg(scope_id), sqlc.arg(key), sqlc.arg(value), sqlc.arg(updated_at_ms))
ON CONFLICT (scope_id, key) DO UPDATE SET value = excluded.value, updated_at_ms = excluded.updated_at_ms;

-- name: DeleteSetting :exec
DELETE FROM settings WHERE scope_id = sqlc.arg(scope_id) AND key = sqlc.arg(key);

-- name: UpsertSettingsRevision :exec
-- Sets the revision of a scope and keeps its digest.
INSERT INTO settings_state (scope_id, revision, static_digest)
VALUES (sqlc.arg(scope_id), sqlc.arg(revision), '')
ON CONFLICT (scope_id) DO UPDATE SET revision = excluded.revision;

-- name: UpsertSettingsDigest :exec
-- Sets the digest of the static layer and the revision of the installation.
INSERT INTO settings_state (scope_id, revision, static_digest)
VALUES (sqlc.arg(scope_id), sqlc.arg(revision), sqlc.arg(static_digest))
ON CONFLICT (scope_id) DO UPDATE SET revision = excluded.revision, static_digest = excluded.static_digest;

-- name: GetUnitSettings :one
-- The namespace is part of the key: a row of the same unit ID in another
-- namespace reads as no row.
SELECT s.unit_id, n.name AS namespace, s.version, s.body, s.updated_at_ms
FROM unit_settings AS s
JOIN namespaces AS n ON n.id = s.namespace_id
WHERE n.name = sqlc.arg(namespace) AND s.unit_id = sqlc.arg(unit_id);

-- name: InsertUnitSettings :execrows
-- The caller checks the fence in the same transaction. A row that exists for
-- the unit is not written: the count is 0.
INSERT INTO unit_settings (unit_id, namespace_id, version, body, updated_at_ms)
VALUES (
    sqlc.arg(unit_id), (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)), 1,
    sqlc.arg(body), sqlc.arg(updated_at_ms)
)
ON CONFLICT (unit_id) DO NOTHING;

-- name: UpdateUnitSettings :execrows
-- Replaces the row that the caller read: the version must be the one that the
-- caller read, and the namespace must match. It sets the version one above.
UPDATE unit_settings
SET body = sqlc.arg(body), version = sqlc.arg(expected)::bigint + 1, updated_at_ms = sqlc.arg(updated_at_ms)
WHERE unit_id = sqlc.arg(unit_id) AND version = sqlc.arg(expected)::bigint
  AND namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace));

-- name: DeleteUnitSettings :execrows
-- Deletes the row that the caller read, and no other row.
DELETE FROM unit_settings
WHERE unit_id = sqlc.arg(unit_id) AND version = sqlc.arg(expected)::bigint
  AND namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace));

-- name: DeleteUnitSettingsOfUnit :exec
-- Removes the settings of a purged unit, in the same transaction as the purge.
DELETE FROM unit_settings
WHERE unit_id = sqlc.arg(unit_id)
  AND namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace));

-- name: GetUnitDefaults :one
SELECT n.name AS namespace, d.pool, d.version, d.updated_at_ms
FROM unit_defaults AS d
JOIN namespaces AS n ON n.id = d.namespace_id
WHERE n.name = sqlc.arg(namespace);

-- name: InsertUnitDefaults :execrows
-- The caller checks the fence in the same transaction. A row that exists is
-- not written: the count is 0.
INSERT INTO unit_defaults (namespace_id, pool, version, updated_at_ms)
VALUES (
    (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)), sqlc.arg(pool), 1,
    sqlc.arg(updated_at_ms)
)
ON CONFLICT (namespace_id) DO NOTHING;

-- name: UpdateUnitDefaults :execrows
-- Replaces the row that the caller read: the version must be the one that the
-- caller read. It sets the version one above.
UPDATE unit_defaults
SET pool = sqlc.arg(pool), version = sqlc.arg(expected)::bigint + 1, updated_at_ms = sqlc.arg(updated_at_ms)
WHERE version = sqlc.arg(expected)::bigint
  AND namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace));

-- name: CreatePlacementRule :exec
-- The caller checks the fence in the same transaction and renumbers the list
-- after the insert.
INSERT INTO placement_rules (id, namespace, position, tool, project, stack, collection_key, created_by, created_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetPlacementRule :one
SELECT r.id, r.namespace, r.position, r.tool, r.project, r.stack, c.name AS collection_name, r.created_by, r.created_at_ms
FROM placement_rules AS r
JOIN collections AS c ON c.namespace = r.namespace AND c.name_key = r.collection_key
WHERE r.namespace = $1 AND r.id = $2;

-- name: ListPlacementRules :many
SELECT r.id, r.namespace, r.position, r.tool, r.project, r.stack, c.name AS collection_name, r.created_by, r.created_at_ms
FROM placement_rules AS r
JOIN collections AS c ON c.namespace = r.namespace AND c.name_key = r.collection_key
WHERE r.namespace = $1
ORDER BY r.position, r.id;

-- name: ListPlacementRuleIDs :many
-- The IDs of the rules of a namespace in list order.
SELECT id
FROM placement_rules
WHERE namespace = $1
ORDER BY position, id;

-- name: UpdatePlacementRuleContent :execrows
UPDATE placement_rules
SET tool = $1, project = $2, stack = $3, collection_key = $4
WHERE namespace = $5 AND id = $6;

-- name: SetPlacementRulePosition :exec
UPDATE placement_rules
SET position = $1
WHERE namespace = $2 AND id = $3;

-- name: DeletePlacementRule :execrows
DELETE FROM placement_rules
WHERE namespace = $1 AND id = $2;

-- name: ListPlacementRulesOfCollection :many
-- The rules that name one collection, for the audit entry of its delete.
SELECT id, tool, project, stack
FROM placement_rules
WHERE namespace = $1 AND collection_key = $2
ORDER BY position, id;

-- name: DeletePlacementRulesOfCollection :exec
DELETE FROM placement_rules
WHERE namespace = $1 AND collection_key = $2;

-- name: CreateStoreCheck :execrows
INSERT INTO store_checks (id, target, state, requested_by, started_at_ms, ended_at_ms, error, report)
VALUES ($1, $2, 'running', $3, $4, NULL, '', '')
ON CONFLICT DO NOTHING;

-- name: GetStoreCheck :one
SELECT id, target, state, requested_by, started_at_ms, ended_at_ms, error, report
FROM store_checks
WHERE id = $1;

-- name: ListStoreChecks :many
SELECT id, target, state, requested_by, started_at_ms, ended_at_ms, error, report
FROM store_checks
ORDER BY started_at_ms DESC, id ASC;

-- name: FinishStoreCheck :execrows
UPDATE store_checks
SET state = $1, error = $2, report = $3, ended_at_ms = $4
WHERE id = $5 AND state = 'running';

-- name: CountStoreChecksEndedSince :one
-- The checks of one target that ended after the given time. A running check
-- has no end time and is not counted.
SELECT count(*)
FROM store_checks
WHERE target = $1 AND state <> 'running' AND ended_at_ms > $2;

-- name: FailEndedStoreChecks :execrows
-- Marks failed each running check whose operation ended failed or canceled
-- without a finish write. The error text is a fixed text of the caller: the
-- last error of the operation never reaches the row.
UPDATE store_checks AS c
SET state = 'failed',
    error = sqlc.arg('error')::text,
    ended_at_ms = sqlc.arg('ended_at_ms')
FROM durable_operations AS o
WHERE c.state = 'running'
  AND o.start_key = sqlc.arg('prefix')::text || c.id
  AND o.kind = sqlc.arg('kind')
  AND o.state IN ('failed', 'canceled');

-- name: InsertSelfServiceBlueprint :execrows
-- The caller checks the fence in the same transaction. A blueprint of the same
-- name in the namespace is not written.
INSERT INTO self_service_blueprints (namespace_id, name, draft_version, draft_json, draft_updated_at_ms)
VALUES (
    (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)),
    sqlc.arg(name), 1, sqlc.arg(draft_json), sqlc.arg(draft_updated_at_ms)
)
ON CONFLICT (namespace_id, name) DO NOTHING;

-- name: UpdateSelfServiceBlueprintDraft :execrows
-- The draft changes only at the version that the caller read.
UPDATE self_service_blueprints
SET draft_version = draft_version + 1, draft_json = sqlc.arg(draft_json), draft_updated_at_ms = sqlc.arg(draft_updated_at_ms)
WHERE namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace))
  AND self_service_blueprints.name = sqlc.arg(name) AND draft_version = sqlc.arg(draft_version);

-- name: CountSelfServiceBlueprints :one
SELECT COUNT(*) FROM self_service_blueprints
WHERE namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace));

-- name: GetSelfServiceBlueprintDraft :one
SELECT b.draft_version, b.draft_json
FROM self_service_blueprints AS b
JOIN namespaces AS n ON n.id = b.namespace_id
WHERE n.name = sqlc.arg(namespace) AND b.name = sqlc.arg(name);

-- name: GetSelfServiceBlueprintVersionOfDraft :one
-- The number of the version that published a draft version. No row means the
-- draft version has no version.
SELECT v.number
FROM self_service_blueprint_versions AS v
JOIN namespaces AS n ON n.id = v.namespace_id
WHERE n.name = sqlc.arg(namespace) AND v.name = sqlc.arg(name) AND v.draft_version = sqlc.arg(draft_version);

-- name: LastSelfServiceBlueprintVersion :one
-- The last published number of a blueprint, 0 for none. The caller holds the
-- write lock of the namespace, so the next number is exact.
SELECT CAST(COALESCE(MAX(v.number), 0) AS BIGINT) AS number
FROM self_service_blueprint_versions AS v
JOIN namespaces AS n ON n.id = v.namespace_id
WHERE n.name = sqlc.arg(namespace) AND v.name = sqlc.arg(name);

-- name: InsertSelfServiceBlueprintVersion :exec
INSERT INTO self_service_blueprint_versions (
    namespace_id, name, number, draft_version, document_json, expr_version, published_by, published_at_ms, retired
)
VALUES (
    (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace)),
    sqlc.arg(name), sqlc.arg(number), sqlc.arg(draft_version), sqlc.arg(document_json), sqlc.arg(expr_version),
    sqlc.arg(published_by), sqlc.arg(published_at_ms), 0
);

-- name: GetSelfServiceBlueprintVersion :one
SELECT n.name AS namespace, v.name, v.number, v.draft_version, v.document_json, v.expr_version, v.published_by,
       v.published_at_ms, v.retired
FROM self_service_blueprint_versions AS v
JOIN namespaces AS n ON n.id = v.namespace_id
WHERE n.name = sqlc.arg(namespace) AND v.name = sqlc.arg(name) AND v.number = sqlc.arg(number);

-- name: ListSelfServiceBlueprintLatest :many
-- The newest published version of each blueprint of a namespace, by name.
SELECT n.name AS namespace, v.name, v.number, v.draft_version, v.document_json, v.expr_version, v.published_by,
       v.published_at_ms, v.retired
FROM self_service_blueprint_versions AS v
JOIN namespaces AS n ON n.id = v.namespace_id
WHERE n.name = sqlc.arg(namespace)
  AND v.number = (
      SELECT MAX(w.number) FROM self_service_blueprint_versions AS w
      WHERE w.namespace_id = v.namespace_id AND w.name = v.name
  )
ORDER BY v.name;

-- name: RetireSelfServiceBlueprintVersion :execrows
-- A retire of a retired version matches the row and changes nothing.
UPDATE self_service_blueprint_versions
SET retired = 1
WHERE namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace))
  AND self_service_blueprint_versions.name = sqlc.arg(name) AND number = sqlc.arg(number);

-- name: SetSelfServiceBlueprintExprVersion :execrows
UPDATE self_service_blueprint_versions
SET expr_version = sqlc.arg(expr_version)
WHERE namespace_id = (SELECT n.id FROM namespaces AS n WHERE n.name = sqlc.arg(namespace))
  AND self_service_blueprint_versions.name = sqlc.arg(name) AND number = sqlc.arg(number);

-- name: GetNamespaceByID :one
-- The namespace with the stable ID id.
SELECT name, primary_installation, failover_version, created_at_ms, allow_forced_writes, allow_unlocked_writes, id
FROM namespaces
WHERE id = sqlc.arg(id);

-- name: ListIssuerKeys :many
-- Every key of the issuer of a namespace, also the deleted keys, oldest first.
SELECT id, state, public_key, sealed, created_at_ms, activated_at_ms, retired_at_ms, deleted_at_ms
FROM issuer_keys
WHERE namespace_id = sqlc.arg(namespace_id)
ORDER BY created_at_ms, id;

-- name: InsertIssuerKey :exec
INSERT INTO issuer_keys (
    namespace_id, id, state, public_key, sealed, created_at_ms, activated_at_ms, retired_at_ms, deleted_at_ms
) VALUES (
    sqlc.arg(namespace_id), sqlc.arg(id), sqlc.arg(state), sqlc.arg(public_key), sqlc.arg(sealed),
    sqlc.arg(created_at_ms), sqlc.arg(activated_at_ms), sqlc.arg(retired_at_ms), sqlc.arg(deleted_at_ms)
);

-- name: UpdateIssuerKey :execrows
-- The new state, times and sealed key of one key. The caller checked the move.
UPDATE issuer_keys
SET state = sqlc.arg(state), sealed = sqlc.arg(sealed), activated_at_ms = sqlc.arg(activated_at_ms),
    retired_at_ms = sqlc.arg(retired_at_ms), deleted_at_ms = sqlc.arg(deleted_at_ms)
WHERE namespace_id = sqlc.arg(namespace_id) AND id = sqlc.arg(id) AND state = sqlc.arg(from_state);

-- name: GetIssuerSettings :one
SELECT issuer_url, subject_template, allow_block_subjects, include_actor_name, version, updated_at_ms, updated_by,
    copy_check
FROM issuer_settings
WHERE namespace_id = sqlc.arg(namespace_id);

-- name: UpsertIssuerSettings :execrows
-- Stores the settings at the version new_version when the stored version is
-- the one before it. A namespace with no row has version 0. It keeps the
-- stored copy check when the issuer URL stays, and clears it when the URL
-- changes: a check of one address says nothing about another.
INSERT INTO issuer_settings (
    namespace_id, issuer_url, subject_template, allow_block_subjects, include_actor_name, version, updated_at_ms,
    updated_by, copy_check
) VALUES (
    sqlc.arg(namespace_id), sqlc.arg(issuer_url), sqlc.arg(subject_template), sqlc.arg(allow_block_subjects),
    sqlc.arg(include_actor_name), sqlc.arg(new_version), sqlc.arg(updated_at_ms), sqlc.arg(updated_by), ''
)
ON CONFLICT (namespace_id) DO UPDATE SET
    issuer_url = excluded.issuer_url, subject_template = excluded.subject_template,
    allow_block_subjects = excluded.allow_block_subjects, include_actor_name = excluded.include_actor_name,
    version = excluded.version, updated_at_ms = excluded.updated_at_ms, updated_by = excluded.updated_by,
    copy_check = CASE WHEN issuer_settings.issuer_url = excluded.issuer_url THEN issuer_settings.copy_check ELSE '' END
WHERE issuer_settings.version = excluded.version - 1;

-- name: ListIssuerURLs :many
-- Every issuer URL that a namespace sets, ordered. The server compares them
-- with the issuer of a trust rule.
SELECT DISTINCT issuer_url FROM issuer_settings WHERE issuer_url <> '' ORDER BY issuer_url;

-- name: UpdateIssuerCopyCheck :execrows
-- Stores the last copy check when the issuer URL that it checked is the stored
-- one. It keeps the version, so a copy check never changes the entity tag of
-- the settings. No row, or another URL, writes nothing.
UPDATE issuer_settings SET copy_check = sqlc.arg(copy_check)
WHERE namespace_id = sqlc.arg(namespace_id) AND issuer_url = sqlc.arg(issuer_url);

-- name: InsertTrustRule :execrows
-- The caller checks the fence and the principal in the same transaction. A
-- rule with a taken ID, or with a name that the namespace has, is not written.
INSERT INTO trust_rules (
    id, namespace, name, issuer, audience, key_source, static_keys, conditions_json, principal_id,
    created_by, created_at_ms
)
VALUES (
    sqlc.arg(id), sqlc.arg(namespace), sqlc.arg(name), sqlc.arg(issuer), sqlc.arg(audience),
    sqlc.arg(key_source), sqlc.arg(static_keys), sqlc.arg(conditions_json), sqlc.arg(principal_id),
    sqlc.arg(created_by), sqlc.arg(created_at_ms)
)
ON CONFLICT DO NOTHING;

-- name: CountTrustRulesInNamespace :one
SELECT COUNT(*) FROM trust_rules WHERE namespace = sqlc.arg(namespace);

-- name: GetTrustRulePrincipal :one
-- Reads the principal of a new rule in the transaction of the create, so a
-- delete of the principal cannot pass between the check and the insert.
SELECT kind, namespace, COALESCE(deleted_at_ms, 0) AS deleted_at_ms
FROM principals
WHERE id = sqlc.arg(id)
FOR SHARE;

-- name: GetTrustRule :one
-- The namespace is part of the key: a rule of another namespace reads as no
-- row.
SELECT id, namespace, name, issuer, audience, key_source, static_keys, conditions_json, principal_id,
    created_by, created_at_ms
FROM trust_rules
WHERE namespace = sqlc.arg(namespace) AND id = sqlc.arg(id);

-- name: ListTrustRulesOfNamespace :many
SELECT id, namespace, name, issuer, audience, key_source, static_keys, conditions_json, principal_id,
    created_by, created_at_ms
FROM trust_rules
WHERE namespace = sqlc.arg(namespace)
ORDER BY name COLLATE "C", id COLLATE "C";

-- name: DeleteTrustRule :execrows
DELETE FROM trust_rules WHERE namespace = sqlc.arg(namespace) AND id = sqlc.arg(id);

-- name: GetExchangePrincipal :one
-- Reads the principal of a token exchange in its transaction, so a delete or
-- a disable of the principal cannot pass between the check and the insert of
-- the token.
SELECT kind, namespace, COALESCE(deleted_at_ms, 0) AS deleted_at_ms, COALESCE(disabled_at_ms, 0) AS disabled_at_ms
FROM principals
WHERE id = sqlc.arg(id)
FOR SHARE;

-- name: GetLocalUserPrincipal :one
-- The kind, the break-glass flag and the subject of a principal that exists,
-- for the checks of a local user write. The row stays locked until the end of
-- the transaction, so two writes that change the credentials of one person
-- run one after the other.
SELECT kind, break_glass, subject FROM principals WHERE id = $1 AND deleted_at_ms IS NULL FOR UPDATE;

-- name: CreateLocalUser :execrows
INSERT INTO local_users (id, principal_id, user_name, password_hash, password_set_at_ms, change_required, demo, created_by, created_at_ms)
VALUES ($1, $2, $3, '', 0, false, false, $4, $5)
ON CONFLICT DO NOTHING;

-- name: CountLocalUserConflicts :one
-- The local users that hold the ID, the principal or the user name.
SELECT COUNT(*) FROM local_users
WHERE id = sqlc.arg('id') OR principal_id = sqlc.arg('principal_id') OR user_name = sqlc.arg('user_name');

-- name: GetLocalUserByName :one
-- The user, its hash and the session generation of its person, in one read:
-- a sign-in seals this generation, so a write that bumps the generation after
-- this read ends the session that the sign-in makes. A person with no row of
-- generation has the generation 1.
SELECT u.id, u.principal_id, u.user_name, u.password_hash, u.password_set_at_ms, u.change_required, u.demo,
  u.created_by, u.created_at_ms,
  CAST(COALESCE(g.generation, 1) AS bigint) AS session_generation
FROM local_users u LEFT JOIN principal_session_generations g ON g.principal_id = u.principal_id
WHERE u.user_name = $1;

-- name: GetLocalUserByPrincipal :one
SELECT id, principal_id, user_name, password_hash, password_set_at_ms, change_required, demo, created_by, created_at_ms
FROM local_users WHERE principal_id = $1;

-- name: GetLocalUser :one
SELECT id, principal_id, user_name, password_hash, password_set_at_ms, change_required, demo, created_by, created_at_ms
FROM local_users WHERE id = $1;

-- name: CountLocalUserName :one
-- The other local users that hold the user name.
SELECT COUNT(*) FROM local_users WHERE user_name = sqlc.arg('user_name') AND id <> sqlc.arg('id');

-- name: RenameLocalUser :execrows
UPDATE local_users SET user_name = $1 WHERE id = $2;

-- name: DeleteLocalUser :execrows
DELETE FROM local_users WHERE id = $1;

-- name: DeleteLocalUserResets :exec
DELETE FROM local_user_resets WHERE user_id = $1;

-- name: SetLocalUserPassword :execrows
UPDATE local_users
SET password_hash = sqlc.arg('password_hash'), password_set_at_ms = sqlc.arg('set_at_ms'),
  change_required = sqlc.arg('change_required')
WHERE id = sqlc.arg('id');

-- name: SetLocalUserChangeRequired :execrows
UPDATE local_users SET change_required = $1 WHERE id = $2 AND password_hash <> '';

-- name: RehashLocalUserPassword :execrows
-- Replaces the hash only while the stored hash is still the old one.
UPDATE local_users SET password_hash = sqlc.arg('new_hash')
WHERE id = sqlc.arg('id') AND password_hash = sqlc.arg('old_hash') AND password_hash <> '';

-- name: CreateDemoLocalUser :execrows
INSERT INTO local_users (id, principal_id, user_name, password_hash, password_set_at_ms, change_required, demo, created_by, created_at_ms)
VALUES ($1, $2, $3, '', 0, false, true, $4, $5)
ON CONFLICT DO NOTHING;

-- name: BumpSessionGeneration :execrows
-- Adds 1 to the session generation of a principal, so that each console
-- session of the principal ends. A principal with no row has the generation
-- 1, so the first bump writes 2.
INSERT INTO principal_session_generations (principal_id, generation)
VALUES ($1, 2)
ON CONFLICT (principal_id) DO UPDATE SET generation = principal_session_generations.generation + 1;

-- name: BumpSessionGenerationFrom :execrows
-- Adds 1 to the stored session generation of a principal only while it is
-- the expected one: a compare-and-swap. The update locks the row and checks
-- the condition again on its latest version, so two bumps from one
-- generation never both pass. A principal with no row writes no row here.
UPDATE principal_session_generations SET generation = generation + 1
WHERE principal_id = sqlc.arg('principal_id') AND generation = sqlc.arg('expected')::bigint;

-- name: InsertFirstSessionGeneration :execrows
-- Writes the generation 2 for a principal with no row, which has the
-- generation 1: the compare-and-swap from the generation 1. A row that
-- exists, or that a concurrent bump wrote first, writes nothing.
INSERT INTO principal_session_generations (principal_id, generation)
VALUES ($1, 2)
ON CONFLICT (principal_id) DO NOTHING;

-- name: GetSessionGeneration :one
-- The session generation of a principal that exists: 1 when it has no row.
SELECT CAST(COALESCE(g.generation, 1) AS bigint) AS generation
FROM principals p LEFT JOIN principal_session_generations g ON g.principal_id = p.id
WHERE p.id = $1 AND p.deleted_at_ms IS NULL;

-- name: RevokePrincipalAccessTokens :execrows
-- Ends every live access token of a principal.
UPDATE access_tokens SET revoked_at_ms = sqlc.arg('revoked_at_ms')::bigint
WHERE principal_id = sqlc.arg('principal_id') AND revoked_at_ms IS NULL;

-- name: CreateLocalUserReset :execrows
INSERT INTO local_user_resets (id, user_id, principal_id, digest, purpose, created_by, reason, revoke_access_tokens, created_at_ms, expires_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT DO NOTHING;

-- name: GetLocalUserResetByDigest :one
SELECT id, user_id, principal_id, digest, purpose, created_by, reason, revoke_access_tokens, created_at_ms, expires_at_ms
FROM local_user_resets WHERE digest = $1;

-- name: DeleteLocalUserReset :execrows
DELETE FROM local_user_resets WHERE id = $1;

-- name: DeleteExpiredLocalUserReset :execrows
DELETE FROM local_user_resets WHERE digest = sqlc.arg('digest') AND expires_at_ms <= sqlc.arg('at_ms');

-- name: PruneLocalUserResets :execrows
DELETE FROM local_user_resets WHERE expires_at_ms <= $1;

-- name: CountLivePasskeys :one
-- The passkeys of a person that are not suspended, read under the lock of the
-- principal. A suspended passkey cannot sign in, so the rule of the last
-- credential counts only these.
SELECT COUNT(*) FROM passkeys WHERE principal_id = $1 AND suspended_at_ms = 0;

-- name: CountOpenPasskeyChallenges :one
-- The challenges of one kind that have not expired at the given time.
SELECT COUNT(*) FROM passkey_challenges WHERE kind = $1 AND expires_at_ms > $2;

-- name: CountOpenPasskeyChallengesBySource :one
-- The challenges of one kind from one source that have not expired at the
-- given time.
SELECT COUNT(*) FROM passkey_challenges WHERE kind = $1 AND source = $2 AND expires_at_ms > $3;

-- name: CountOpenPasskeyChallengesBySourceGroup :one
-- The challenges of one kind from one site group that have not expired at the
-- given time.
SELECT COUNT(*) FROM passkey_challenges WHERE kind = $1 AND source_group = $2 AND expires_at_ms > $3;

-- name: CountPasskeys :one
-- The passkeys of a person, read under the lock of the principal.
SELECT COUNT(*) FROM passkeys WHERE principal_id = $1;

-- name: CreatePasskey :execrows
INSERT INTO passkeys (id, principal_id, name, credential_id, public_key, transports, aaguid, sign_count,
                      backup_eligible, created_at_ms, last_used_at_ms, suspended_at_ms)
VALUES (sqlc.arg('id'), sqlc.arg('principal_id'), sqlc.arg('name'), sqlc.arg('credential_id'), sqlc.arg('public_key'),
        sqlc.arg('transports'), sqlc.arg('aaguid'), sqlc.arg('sign_count'), sqlc.arg('backup_eligible'),
        sqlc.arg('created_at_ms'), 0, 0)
ON CONFLICT DO NOTHING;

-- name: CreatePasskeyChallenge :execrows
INSERT INTO passkey_challenges (digest, kind, principal_id, pending_id, session_id, source, source_group,
                                created_at_ms, expires_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT DO NOTHING;

-- name: DeleteExpiredPasskeyChallenges :execrows
DELETE FROM passkey_challenges WHERE expires_at_ms <= $1;

-- name: DeletePasskey :execrows
DELETE FROM passkeys WHERE id = sqlc.arg('id') AND principal_id = sqlc.arg('principal_id');

-- name: GetPasskey :one
SELECT * FROM passkeys WHERE id = $1;

-- name: GetPasskeyByCredentialID :one
SELECT * FROM passkeys WHERE credential_id = $1;

-- name: ListPasskeys :many
SELECT * FROM passkeys WHERE principal_id = $1 ORDER BY created_at_ms, id COLLATE "C";

-- name: RecordPasskeyUse :execrows
-- The use of a passkey that is not suspended, when the new counter is above
-- the stored counter or both are 0. A use that changes no row is a
-- regression, a suspended passkey or no passkey. The update takes the lock of
-- the row, so of two uses with one counter, one passes.
UPDATE passkeys SET sign_count = sqlc.arg('sign_count'), last_used_at_ms = sqlc.arg('at_ms')
WHERE id = sqlc.arg('id') AND suspended_at_ms = 0
  AND (sign_count < sqlc.arg('sign_count') OR (sign_count = 0 AND sqlc.arg('sign_count')::bigint = 0));

-- name: SuspendPasskey :execrows
UPDATE passkeys SET suspended_at_ms = sqlc.arg('at_ms') WHERE id = sqlc.arg('id') AND suspended_at_ms = 0;

-- name: TakePasskeyChallenge :one
-- The delete of a challenge returns it, so that a challenge works once,
-- also after its expiry: the caller checks the expiry.
DELETE FROM passkey_challenges WHERE digest = $1 RETURNING *;

-- name: EnsureNameLock :exec
-- Makes the count of a name and a sub-key in a scope when it does not exist,
-- so the locked read that follows finds a row.
INSERT INTO name_locks (scope, name, sub) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING;

-- name: GetNameLockForUpdate :one
-- Locks the row until the end of the transaction, so concurrent failures of
-- one name count one after the other.
SELECT sub, failures, locked_until_ms, level, last_failure_ms, last_lock_end_ms, reset_after_ms
FROM name_locks WHERE scope = $1 AND name = $2 AND sub = $3 FOR UPDATE;

-- name: SetNameLock :exec
UPDATE name_locks SET failures = sqlc.arg('failures'), locked_until_ms = sqlc.arg('locked_until_ms'),
  level = sqlc.arg('level'), last_failure_ms = sqlc.arg('last_failure_ms'),
  last_lock_end_ms = sqlc.arg('last_lock_end_ms'), reset_after_ms = sqlc.arg('reset_after_ms')
WHERE scope = sqlc.arg('scope') AND name = sqlc.arg('name') AND sub = sqlc.arg('sub');

-- name: ClearNameLockSub :exec
-- Sets the count to 0 and ends the lock. The level stays.
UPDATE name_locks SET failures = 0, locked_until_ms = 0 WHERE scope = $1 AND name = $2 AND sub = $3;

-- name: ClearNameLocks :execrows
-- Sets the count of each sub-key of a name to 0 and ends each lock.
UPDATE name_locks SET failures = 0, locked_until_ms = 0 WHERE scope = $1 AND name = $2;

-- name: ListNameLocks :many
SELECT sub, failures, locked_until_ms, level, last_failure_ms, last_lock_end_ms, reset_after_ms
FROM name_locks WHERE scope = $1 AND name = $2 ORDER BY sub;

-- name: PruneEndedNameLocks :many
-- Deletes at most max_rows counts whose last failure is at or before
-- before_ms, whose lock ended at now_ms and that no attempt saw (their
-- locked_until_ms is not 0), and whose level is 0 or past its reset time. The
-- select skips a row that a failure locks now, and the delete checks the
-- conditions again on the row that it deletes. It returns the scope and the
-- end of the lock of each deleted count.
DELETE FROM name_locks
WHERE (scope, name, sub) IN (
    SELECT p.scope, p.name, p.sub FROM name_locks p
    WHERE p.last_failure_ms <= sqlc.arg('before_ms')::bigint AND p.locked_until_ms <> 0
      AND p.locked_until_ms <= sqlc.arg('now_ms')::bigint
      AND (p.level = 0 OR p.last_lock_end_ms + p.reset_after_ms <= sqlc.arg('now_ms')::bigint)
    LIMIT sqlc.arg('max_rows')::bigint
    FOR UPDATE SKIP LOCKED
)
  AND last_failure_ms <= sqlc.arg('before_ms')::bigint AND locked_until_ms <> 0
  AND locked_until_ms <= sqlc.arg('now_ms')::bigint
  AND (level = 0 OR last_lock_end_ms + reset_after_ms <= sqlc.arg('now_ms')::bigint)
RETURNING scope, locked_until_ms;

-- name: PruneNameLocks :execrows
-- Deletes at most max_rows counts with no lock (locked_until_ms is 0) whose
-- last failure is at or before before_ms and whose level is 0 or past its
-- reset time. A count whose lock ended and that no attempt saw goes through
-- PruneEndedNameLocks, so the end of its lock has an entry. The select skips
-- a row that a failure locks now, and the delete checks the conditions again
-- on the row that it deletes.
DELETE FROM name_locks
WHERE (scope, name, sub) IN (
    SELECT p.scope, p.name, p.sub FROM name_locks p
    WHERE p.last_failure_ms <= sqlc.arg('before_ms')::bigint AND p.locked_until_ms = 0
      AND (p.level = 0 OR p.last_lock_end_ms + p.reset_after_ms <= sqlc.arg('now_ms')::bigint)
    LIMIT sqlc.arg('max_rows')::bigint
    FOR UPDATE SKIP LOCKED
)
  AND last_failure_ms <= sqlc.arg('before_ms')::bigint AND locked_until_ms = 0
  AND (level = 0 OR last_lock_end_ms + reset_after_ms <= sqlc.arg('now_ms')::bigint);

-- name: InsertNameLockKey :exec
INSERT INTO name_lock_key (id, key) VALUES (1, $1) ON CONFLICT DO NOTHING;

-- name: GetNameLockKey :one
SELECT key FROM name_lock_key WHERE id = 1;

-- name: GetPrincipalState :one
-- The state of a principal that each request with an access cookie reads:
-- the owner, its activity by the account rule of the inactivity disable, the
-- policies, and the session generation, from one read. A principal with no
-- row of principal_session_generations has the generation 1.
-- The live check is num_nulls(p.deleted_at_ms) = 1, as in GetSessionByDigest.
SELECT p.id, p.subject, p.display_name, p.kind AS principal_kind,
  p.disabled_at_ms AS principal_disabled_at_ms, p.break_glass AS principal_break_glass,
  GREATEST(p.created_at_ms, p.last_active_at_ms, COALESCE(p.last_sign_in_at_ms, 0), COALESCE((
    SELECT MAX(GREATEST(o.created_at_ms, COALESCE(o.last_used_at_ms, 0))) FROM access_tokens o WHERE o.principal_id = p.id
  ), 0), COALESCE((
    SELECT MAX(x.last_seen_at_ms) FROM sessions x WHERE x.principal_id = p.id
  ), 0))::bigint AS principal_active_at_ms,
  sp.idle_ms AS policy_idle_ms, sp.max_length_ms AS policy_max_length_ms, sp.updated_at_ms AS policy_updated_at_ms,
  sp.token_freshness_ms AS policy_token_freshness_ms,
  ap.inactivity_ms AS accounts_inactivity_ms, ap.disable_accounts AS accounts_disable,
  COALESCE(g.generation, 1)::bigint AS principal_generation
FROM principals p
LEFT JOIN principal_session_generations g ON g.principal_id = p.id
CROSS JOIN session_policy sp
CROSS JOIN access_token_policy ap
WHERE p.id = $1 AND num_nulls(p.deleted_at_ms) = 1 AND sp.id = 1 AND ap.id = 1;
