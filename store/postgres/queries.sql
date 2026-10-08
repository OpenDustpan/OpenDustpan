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
