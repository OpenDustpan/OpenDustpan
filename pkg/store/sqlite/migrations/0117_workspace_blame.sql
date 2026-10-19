-- The blame of OpenTofu workspaces: what each state version did to each
-- resource instance. workspace_blame_cursors holds the last ordinal that the
-- blame operation compared, and workspace_blame_items one row for each state
-- version that it compared, so an opaque state version and a lineage change
-- keep their mark when the state version has no row. A resource change holds
-- no value and no path of a secret value. The ordinal is the place of the
-- state version in the order of finalizes of its workspace, from 1.
CREATE TABLE workspace_blame_cursors (
    workspace_id TEXT    NOT NULL PRIMARY KEY REFERENCES workspaces (id),
    ordinal      INTEGER NOT NULL CHECK (ordinal >= 1)
) STRICT;

CREATE TABLE workspace_blame_items (
    workspace_id     TEXT    NOT NULL REFERENCES workspaces (id),
    state_version_id TEXT    NOT NULL,
    ordinal          INTEGER NOT NULL CHECK (ordinal >= 1),
    opaque           INTEGER NOT NULL CHECK (opaque IN (0, 1)),
    lineage_changed  INTEGER NOT NULL CHECK (lineage_changed IN (0, 1)),
    resource_count   INTEGER NOT NULL CHECK (resource_count >= 0),
    PRIMARY KEY (workspace_id, state_version_id),
    UNIQUE (workspace_id, ordinal)
) STRICT;

CREATE TABLE workspace_resource_changes (
    workspace_id     TEXT    NOT NULL,
    state_version_id TEXT    NOT NULL,
    address          TEXT    NOT NULL,
    ordinal          INTEGER NOT NULL,
    kind             TEXT    NOT NULL CHECK (kind IN ('created', 'updated', 'replaced', 'deleted')),
    at_ms            INTEGER NOT NULL,
    by_subject       TEXT    NOT NULL,
    by_token_id      TEXT    NOT NULL,
    by_job_id        TEXT    NOT NULL,
    changed_paths    INTEGER NOT NULL CHECK (changed_paths >= 0),
    restored         INTEGER NOT NULL CHECK (restored IN (0, 1)),
    forced           INTEGER NOT NULL CHECK (forced IN (0, 1)),
    lineage_changed  INTEGER NOT NULL CHECK (lineage_changed IN (0, 1)),
    PRIMARY KEY (workspace_id, state_version_id, address),
    FOREIGN KEY (workspace_id, state_version_id) REFERENCES workspace_blame_items (workspace_id, state_version_id)
) STRICT;

-- The history of one resource instance, newest first.
CREATE INDEX workspace_resource_changes_address ON workspace_resource_changes (workspace_id, address, ordinal DESC);
