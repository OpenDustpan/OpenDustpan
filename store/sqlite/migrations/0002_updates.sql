-- Updates: one run of the Pulumi engine against a stack. Times are Unix
-- milliseconds, 0 when unset. lease_digest is the SHA-256 of the lease token,
-- empty when the update holds no lease.
CREATE TABLE updates (
    namespace           TEXT    NOT NULL,
    project             TEXT    NOT NULL,
    stack               TEXT    NOT NULL,
    id                  TEXT    NOT NULL,
    kind                TEXT    NOT NULL,
    dry_run             INTEGER NOT NULL,
    status              TEXT    NOT NULL,
    version             INTEGER NOT NULL,
    revision            INTEGER NOT NULL,
    lease_digest        BLOB    NOT NULL,
    lease_expires_at_ms INTEGER NOT NULL,
    created_at_ms       INTEGER NOT NULL,
    started_at_ms       INTEGER NOT NULL,
    ended_at_ms         INTEGER NOT NULL,
    PRIMARY KEY (namespace, project, stack, id),
    FOREIGN KEY (namespace, project, stack) REFERENCES stacks (namespace, project, name)
) STRICT;

-- At most one running update per stack changes its state.
CREATE UNIQUE INDEX updates_one_running ON updates (namespace, project, stack)
WHERE status = 'running' AND dry_run = 0 AND kind <> 'preview';

-- History: started mutating updates by version.
CREATE INDEX updates_history ON updates (namespace, project, stack, version)
WHERE status <> 'not started' AND dry_run = 0 AND kind <> 'preview';

-- Checkpoints: the latest state of each stack, as its client sent it. The
-- deployment is stored byte for byte; the server never parses it.
CREATE TABLE checkpoints (
    namespace      TEXT    NOT NULL,
    project        TEXT    NOT NULL,
    stack          TEXT    NOT NULL,
    update_id      TEXT    NOT NULL,
    lease_digest   BLOB    NOT NULL,
    schema_version INTEGER NOT NULL,
    sequence       INTEGER NOT NULL,
    digest         BLOB    NOT NULL,
    invalid        INTEGER NOT NULL,
    features_json  TEXT    NOT NULL,
    deployment     BLOB    NOT NULL,
    saved_at_ms    INTEGER NOT NULL,
    PRIMARY KEY (namespace, project, stack),
    FOREIGN KEY (namespace, project, stack, update_id) REFERENCES updates (namespace, project, stack, id)
) STRICT;

-- Events: engine events of an update, opaque and byte for byte.
CREATE TABLE update_events (
    namespace    TEXT    NOT NULL,
    project      TEXT    NOT NULL,
    stack        TEXT    NOT NULL,
    update_id    TEXT    NOT NULL,
    sequence     INTEGER NOT NULL,
    timestamp_ms INTEGER NOT NULL,
    payload      BLOB    NOT NULL,
    PRIMARY KEY (namespace, project, stack, update_id, sequence),
    FOREIGN KEY (namespace, project, stack, update_id) REFERENCES updates (namespace, project, stack, id)
) STRICT;
