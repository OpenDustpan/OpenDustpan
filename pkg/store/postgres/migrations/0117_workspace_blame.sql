-- The blame of OpenTofu workspaces: what each state version did to each
-- resource instance. workspace_blame_cursors holds the last ordinal that the
-- blame operation compared, and workspace_blame_items one row for each state
-- version that it compared, so an opaque state version and a lineage change
-- keep their mark when the state version has no row. A resource change holds
-- no value and no path of a secret value. The ordinal is the place of the
-- state version in the order of finalizes of its workspace, from 1.
CREATE TABLE workspace_blame_cursors (
    workspace_id text   NOT NULL PRIMARY KEY REFERENCES workspaces (id),
    ordinal      bigint NOT NULL CHECK (ordinal >= 1)
);

CREATE TABLE workspace_blame_items (
    workspace_id     text    NOT NULL REFERENCES workspaces (id),
    state_version_id text    NOT NULL,
    ordinal          bigint  NOT NULL CHECK (ordinal >= 1),
    opaque           boolean NOT NULL,
    lineage_changed  boolean NOT NULL,
    PRIMARY KEY (workspace_id, state_version_id),
    UNIQUE (workspace_id, ordinal)
);

CREATE TABLE workspace_resource_changes (
    workspace_id     text    NOT NULL,
    state_version_id text    NOT NULL,
    address          text    NOT NULL,
    ordinal          bigint  NOT NULL,
    kind             text    NOT NULL CHECK (kind IN ('created', 'updated', 'replaced', 'deleted')),
    at_ms            bigint  NOT NULL,
    by_subject       text    NOT NULL,
    by_token_id      text    NOT NULL,
    by_job_id        text    NOT NULL,
    changed_paths    bigint  NOT NULL CHECK (changed_paths >= 0),
    restored         boolean NOT NULL,
    forced           boolean NOT NULL,
    lineage_changed  boolean NOT NULL,
    PRIMARY KEY (workspace_id, state_version_id, address),
    FOREIGN KEY (workspace_id, state_version_id) REFERENCES workspace_blame_items (workspace_id, state_version_id)
);

-- The history of one resource instance, newest first.
CREATE INDEX workspace_resource_changes_address ON workspace_resource_changes (workspace_id, address, ordinal DESC);
