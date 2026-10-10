-- name: CreateNamespace :execrows
INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING;

-- name: GetNamespace :one
SELECT name, home_region, failover_version, created_at_ms
FROM namespaces
WHERE name = $1;

-- name: GetFailoverVersionForShare :one
-- Locks the namespace row so a concurrent promotion waits for this write.
SELECT failover_version
FROM namespaces
WHERE name = $1
FOR SHARE;

-- name: CreateStack :execrows
INSERT INTO stacks (namespace, project, name, tags, version, created_at_ms, updated_at_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT DO NOTHING;

-- name: GetStack :one
SELECT namespace, project, name, tags, version, created_at_ms, updated_at_ms
FROM stacks
WHERE namespace = $1 AND project = $2 AND name = $3;

-- name: ListStacksInNamespace :many
SELECT namespace, project, name, tags, version, created_at_ms, updated_at_ms
FROM stacks
WHERE namespace = $1
ORDER BY project, name;

-- name: ListStacksInProject :many
SELECT namespace, project, name, tags, version, created_at_ms, updated_at_ms
FROM stacks
WHERE namespace = $1 AND project = $2
ORDER BY name;

-- name: StackExists :one
SELECT EXISTS (
    SELECT 1 FROM stacks WHERE namespace = $1 AND project = $2 AND name = $3
);

-- name: LockStackForUpdates :one
-- Serializes starts and ends of the stack's updates. NO KEY UPDATE does not
-- block the foreign-key checks of checkpoint and event writes.
SELECT version
FROM stacks
WHERE namespace = $1 AND project = $2 AND name = $3
FOR NO KEY UPDATE;

-- name: CreateUpdate :exec
INSERT INTO updates (
    namespace, project, stack, id, kind, dry_run, status, version, revision,
    lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14);

-- name: GetUpdate :one
SELECT namespace, project, stack, id, kind, dry_run, status, version, revision,
       lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
FROM updates
WHERE namespace = $1 AND project = $2 AND stack = $3 AND id = $4;

-- name: SaveUpdate :execrows
-- A compare-and-swap: it changes nothing unless the stored revision is the
-- one the caller read.
UPDATE updates
SET status = @status, version = @version, revision = @revision,
    lease_digest = @lease_digest, lease_expires_at_ms = @lease_expires_at_ms,
    started_at_ms = @started_at_ms, ended_at_ms = @ended_at_ms
WHERE namespace = @namespace AND project = @project AND stack = @stack AND id = @id
  AND revision = @expected_revision;

-- name: RunningMutatingUpdates :one
SELECT COUNT(*)
FROM updates
WHERE namespace = $1 AND project = $2 AND stack = $3
  AND status = 'running' AND NOT dry_run AND kind <> 'preview';

-- name: HighestUpdateVersion :one
SELECT COALESCE(MAX(version), 0)::BIGINT
FROM updates
WHERE namespace = $1 AND project = $2 AND stack = $3
  AND status <> 'not started' AND NOT dry_run AND kind <> 'preview';

-- name: SetStackVersion :exec
UPDATE stacks
SET version = $1, updated_at_ms = $2
WHERE namespace = $3 AND project = $4 AND name = $5;

-- name: ActiveUpdate :one
SELECT namespace, project, stack, id, kind, dry_run, status, version, revision,
       lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
FROM updates
WHERE namespace = $1 AND project = $2 AND stack = $3
  AND status = 'running' AND NOT dry_run AND kind <> 'preview';

-- name: ListUpdates :many
SELECT namespace, project, stack, id, kind, dry_run, status, version, revision,
       lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
FROM updates
WHERE namespace = $1 AND project = $2 AND stack = $3
  AND status <> 'not started' AND NOT dry_run AND kind <> 'preview'
ORDER BY version DESC
LIMIT $4 OFFSET $5;

-- name: UpdateExists :one
SELECT EXISTS (
    SELECT 1 FROM updates WHERE namespace = $1 AND project = $2 AND stack = $3 AND id = $4
);

-- name: GetUpdateLeaseForShare :one
-- Locks the update so a concurrent cancel waits until this write commits.
SELECT status, lease_digest
FROM updates
WHERE namespace = $1 AND project = $2 AND stack = $3 AND id = $4
FOR SHARE;

-- name: GetCheckpoint :one
SELECT namespace, project, stack, update_id, lease_digest, schema_version, sequence,
       digest, invalid, features, deployment, saved_at_ms
FROM checkpoints
WHERE namespace = $1 AND project = $2 AND stack = $3;

-- name: PutCheckpoint :exec
INSERT INTO checkpoints (
    namespace, project, stack, update_id, lease_digest, schema_version, sequence,
    digest, invalid, features, deployment, saved_at_ms
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (namespace, project, stack) DO UPDATE SET
    update_id = excluded.update_id,
    lease_digest = excluded.lease_digest,
    schema_version = excluded.schema_version,
    sequence = excluded.sequence,
    digest = excluded.digest,
    invalid = excluded.invalid,
    features = excluded.features,
    deployment = excluded.deployment,
    saved_at_ms = excluded.saved_at_ms;

-- name: AppendEvent :exec
INSERT INTO update_events (namespace, project, stack, update_id, sequence, timestamp_ms, payload)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT DO NOTHING;

-- name: ListEvents :many
SELECT sequence, timestamp_ms, payload
FROM update_events
WHERE namespace = $1 AND project = $2 AND stack = $3 AND update_id = $4 AND sequence > $5
ORDER BY sequence
LIMIT $6;
