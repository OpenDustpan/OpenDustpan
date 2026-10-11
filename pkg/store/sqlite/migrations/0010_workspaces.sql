-- Workspaces and state versions of OpenTofu (ADR 26): metadata in SQL,
-- content in blobs. A workspace is unique by namespace and name and keeps a
-- stable ID (ADR 19). A state version belongs to one workspace. Timestamps are
-- Unix milliseconds in BIGINT. The upload capability is a digest only.
--
-- The current state is a pointer on the workspace, not a query for the highest
-- finalized serial: a forced upload may carry a lower serial and still become
-- current, so the highest serial is not always the current state. The pointer
-- has no foreign key, to avoid a reference cycle with state_versions.
CREATE TABLE workspaces (
    id                       TEXT    NOT NULL PRIMARY KEY,
    namespace                TEXT    NOT NULL REFERENCES namespaces (name),
    name                     TEXT    NOT NULL,
    revision                 INTEGER NOT NULL,
    lock_id                  TEXT    NOT NULL DEFAULT '',
    lock_holder              TEXT    NOT NULL DEFAULT '',
    lock_reason              TEXT    NOT NULL DEFAULT '',
    locked_at_ms             INTEGER NOT NULL DEFAULT 0,
    current_state_version_id TEXT    NOT NULL DEFAULT '',
    created_at_ms            INTEGER NOT NULL,
    updated_at_ms            INTEGER NOT NULL,
    UNIQUE (namespace, name)
) STRICT;

CREATE TABLE state_versions (
    id                    TEXT    NOT NULL PRIMARY KEY,
    workspace_id          TEXT    NOT NULL REFERENCES workspaces (id),
    status                TEXT    NOT NULL,
    serial                INTEGER NOT NULL,
    lineage               TEXT    NOT NULL,
    md5                   BLOB    NOT NULL,
    size                  INTEGER NOT NULL,
    content_key           TEXT    NOT NULL DEFAULT '',
    outputs               BLOB,
    force                 INTEGER NOT NULL DEFAULT 0,
    upload_digest         BLOB    NOT NULL,
    upload_expires_at_ms  INTEGER NOT NULL,
    holder                TEXT    NOT NULL DEFAULT '',
    lock_id               TEXT    NOT NULL DEFAULT '',
    created_at_ms         INTEGER NOT NULL,
    finalized_at_ms       INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX state_versions_workspace_id ON state_versions (workspace_id);
