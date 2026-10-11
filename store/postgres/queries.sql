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
INSERT INTO stacks (namespace, project, name, id, tags, version, created_at_ms, updated_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT DO NOTHING;

-- name: GetStack :one
SELECT namespace, project, name, id, tags, version, created_at_ms, updated_at_ms
FROM stacks
WHERE namespace = $1 AND project = $2 AND name = $3;

-- name: ListStacksInNamespace :many
SELECT namespace, project, name, id, tags, version, created_at_ms, updated_at_ms
FROM stacks
WHERE namespace = $1
ORDER BY project, name;

-- name: ListStacksInProject :many
SELECT namespace, project, name, id, tags, version, created_at_ms, updated_at_ms
FROM stacks
WHERE namespace = $1 AND project = $2
ORDER BY name;

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
    lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- name: GetUpdate :one
SELECT stack_id, id, kind, dry_run, status, version, revision,
       lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
FROM updates
WHERE stack_id = $1 AND id = $2;

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
SELECT stack_id, id, kind, dry_run, status, version, revision,
       lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
FROM updates
WHERE stack_id = $1
  AND status = 'running' AND NOT dry_run AND kind <> 'preview';

-- name: ListUpdates :many
SELECT stack_id, id, kind, dry_run, status, version, revision,
       lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
FROM updates
WHERE stack_id = $1
  AND status <> 'not started' AND NOT dry_run AND kind <> 'preview'
ORDER BY version DESC
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

-- name: DeleteStack :exec
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
