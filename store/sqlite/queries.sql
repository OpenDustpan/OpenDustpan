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
