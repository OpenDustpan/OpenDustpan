-- name: CreateNamespace :execrows
INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
VALUES (?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetNamespace :one
SELECT name, home_region, failover_version, created_at_ms
FROM namespaces
WHERE name = ?;

-- name: GetFailoverVersion :one
SELECT failover_version
FROM namespaces
WHERE name = ?;

-- name: CreateStack :execrows
INSERT INTO stacks (namespace, project, name, tags_json, version, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetStack :one
SELECT namespace, project, name, tags_json, version, created_at_ms, updated_at_ms
FROM stacks
WHERE namespace = ? AND project = ? AND name = ?;

-- name: ListStacksInNamespace :many
SELECT namespace, project, name, tags_json, version, created_at_ms, updated_at_ms
FROM stacks
WHERE namespace = ?
ORDER BY project, name;

-- name: ListStacksInProject :many
SELECT namespace, project, name, tags_json, version, created_at_ms, updated_at_ms
FROM stacks
WHERE namespace = ? AND project = ?
ORDER BY name;

-- name: StackExists :one
SELECT EXISTS (
    SELECT 1 FROM stacks WHERE namespace = ? AND project = ? AND name = ?
);

-- name: CreateUpdate :exec
INSERT INTO updates (
    namespace, project, stack, id, kind, dry_run, status, version, revision,
    lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetUpdate :one
SELECT namespace, project, stack, id, kind, dry_run, status, version, revision,
       lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
FROM updates
WHERE namespace = ? AND project = ? AND stack = ? AND id = ?;

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
WHERE namespace = ? AND project = ? AND stack = ?
  AND status = 'running' AND dry_run = 0 AND kind <> 'preview';

-- name: HighestUpdateVersion :one
SELECT CAST(COALESCE(MAX(version), 0) AS INTEGER)
FROM updates
WHERE namespace = ? AND project = ? AND stack = ?
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
SELECT namespace, project, stack, id, kind, dry_run, status, version, revision,
       lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
FROM updates
WHERE namespace = ? AND project = ? AND stack = ?
  AND status = 'running' AND dry_run = 0 AND kind <> 'preview';

-- name: ListUpdates :many
SELECT namespace, project, stack, id, kind, dry_run, status, version, revision,
       lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
FROM updates
WHERE namespace = ? AND project = ? AND stack = ?
  AND status <> 'not started' AND dry_run = 0 AND kind <> 'preview'
ORDER BY version DESC
LIMIT ? OFFSET ?;

-- name: GetUpdateLease :one
SELECT status, lease_digest
FROM updates
WHERE namespace = ? AND project = ? AND stack = ? AND id = ?;

-- name: GetCheckpoint :one
SELECT namespace, project, stack, update_id, lease_digest, schema_version, sequence,
       digest, invalid, features_json, deployment, saved_at_ms
FROM checkpoints
WHERE namespace = ? AND project = ? AND stack = ?;

-- name: PutCheckpoint :exec
INSERT INTO checkpoints (
    namespace, project, stack, update_id, lease_digest, schema_version, sequence,
    digest, invalid, features_json, deployment, saved_at_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (namespace, project, stack) DO UPDATE SET
    update_id = excluded.update_id,
    lease_digest = excluded.lease_digest,
    schema_version = excluded.schema_version,
    sequence = excluded.sequence,
    digest = excluded.digest,
    invalid = excluded.invalid,
    features_json = excluded.features_json,
    deployment = excluded.deployment,
    saved_at_ms = excluded.saved_at_ms;

-- name: AppendEvent :exec
INSERT INTO update_events (namespace, project, stack, update_id, sequence, timestamp_ms, payload)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: ListEvents :many
SELECT sequence, timestamp_ms, payload
FROM update_events
WHERE namespace = ? AND project = ? AND stack = ? AND update_id = ? AND sequence > ?
ORDER BY sequence
LIMIT ?;
