-- Updates: one run of the Pulumi engine against a stack. Times are Unix
-- milliseconds, 0 when unset. lease_digest is the SHA-256 of the lease token,
-- empty when the update holds no lease.
CREATE TABLE updates (
    namespace           TEXT    NOT NULL,
    project             TEXT    NOT NULL,
    stack               TEXT    NOT NULL,
    id                  TEXT    NOT NULL,
    kind                TEXT    NOT NULL,
    dry_run             BOOLEAN NOT NULL,
    status              TEXT    NOT NULL,
    version             BIGINT  NOT NULL,
    revision            BIGINT  NOT NULL,
    lease_digest        BYTEA   NOT NULL,
    lease_expires_at_ms BIGINT  NOT NULL,
    created_at_ms       BIGINT  NOT NULL,
    started_at_ms       BIGINT  NOT NULL,
    ended_at_ms         BIGINT  NOT NULL,
    PRIMARY KEY (namespace, project, stack, id),
    FOREIGN KEY (namespace, project, stack) REFERENCES stacks (namespace, project, name)
);

-- At most one running update per stack changes its state.
CREATE UNIQUE INDEX updates_one_running ON updates (namespace, project, stack)
WHERE status = 'running' AND NOT dry_run AND kind <> 'preview';

-- History: started mutating updates by version.
CREATE INDEX updates_history ON updates (namespace, project, stack, version)
WHERE status <> 'not started' AND NOT dry_run AND kind <> 'preview';

-- Checkpoints: the latest state of each stack, as its client sent it. The
-- deployment is BYTEA, not JSONB, so it is stored byte for byte.
CREATE TABLE checkpoints (
    namespace      TEXT    NOT NULL,
    project        TEXT    NOT NULL,
    stack          TEXT    NOT NULL,
    update_id      TEXT    NOT NULL,
    lease_digest   BYTEA   NOT NULL,
    schema_version BIGINT  NOT NULL,
    sequence       BIGINT  NOT NULL,
    digest         BYTEA   NOT NULL,
    invalid        BOOLEAN NOT NULL,
    features       JSONB   NOT NULL,
    deployment     BYTEA   NOT NULL,
    saved_at_ms    BIGINT  NOT NULL,
    PRIMARY KEY (namespace, project, stack),
    FOREIGN KEY (namespace, project, stack, update_id) REFERENCES updates (namespace, project, stack, id)
);

-- Events: engine events of an update, opaque and byte for byte.
CREATE TABLE update_events (
    namespace    TEXT   NOT NULL,
    project      TEXT   NOT NULL,
    stack        TEXT   NOT NULL,
    update_id    TEXT   NOT NULL,
    sequence     BIGINT NOT NULL,
    timestamp_ms BIGINT NOT NULL,
    payload      BYTEA  NOT NULL,
    PRIMARY KEY (namespace, project, stack, update_id, sequence),
    FOREIGN KEY (namespace, project, stack, update_id) REFERENCES updates (namespace, project, stack, id)
);
