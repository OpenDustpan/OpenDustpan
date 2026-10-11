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
    id                       TEXT   NOT NULL PRIMARY KEY,
    namespace                TEXT   NOT NULL REFERENCES namespaces (name),
    name                     TEXT   NOT NULL,
    revision                 BIGINT NOT NULL,
    lock_id                  TEXT   NOT NULL DEFAULT '',
    lock_holder              TEXT   NOT NULL DEFAULT '',
    lock_reason              TEXT   NOT NULL DEFAULT '',
    locked_at_ms             BIGINT NOT NULL DEFAULT 0,
    current_state_version_id TEXT   NOT NULL DEFAULT '',
    created_at_ms            BIGINT NOT NULL,
    updated_at_ms            BIGINT NOT NULL,
    UNIQUE (namespace, name)
);

CREATE TABLE state_versions (
    id                   TEXT   NOT NULL PRIMARY KEY,
    workspace_id         TEXT   NOT NULL REFERENCES workspaces (id),
    status               TEXT   NOT NULL,
    serial               BIGINT NOT NULL,
    lineage              TEXT   NOT NULL,
    md5                  BYTEA  NOT NULL,
    size                 BIGINT NOT NULL,
    content_key          TEXT   NOT NULL DEFAULT '',
    outputs              BYTEA,
    force                BOOLEAN NOT NULL DEFAULT FALSE,
    upload_digest        BYTEA  NOT NULL,
    upload_expires_at_ms BIGINT NOT NULL,
    holder               TEXT   NOT NULL DEFAULT '',
    lock_id              TEXT   NOT NULL DEFAULT '',
    created_at_ms        BIGINT NOT NULL,
    finalized_at_ms      BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX state_versions_workspace_id ON state_versions (workspace_id);
