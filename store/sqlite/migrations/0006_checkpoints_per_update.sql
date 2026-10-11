-- Checkpoints: one row for each mutating update (ADR 24). The key is the
-- stack ID and the update. Each write of the running update overwrites the
-- row of that update. The latest state of a stack is the checkpoint of its
-- highest version. The final delete of a stack deletes its checkpoints, so
-- every child foreign key deletes with its parent.
CREATE TABLE updates_new (
    stack_id            TEXT    NOT NULL REFERENCES stacks (id) ON DELETE CASCADE,
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
    PRIMARY KEY (stack_id, id),
    FOREIGN KEY (stack_id) REFERENCES stacks (id) ON DELETE CASCADE
) STRICT;

INSERT INTO updates_new (
    stack_id, id, kind, dry_run, status, version, revision,
    lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
) SELECT
    stack_id, id, kind, dry_run, status, version, revision,
    lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms
FROM updates;

CREATE TABLE _checkpoints_data AS SELECT
    stack_id, update_id, lease_digest, schema_version, sequence,
    digest, invalid, features_json, deployment, saved_at_ms
FROM checkpoints;

CREATE TABLE _events_data AS SELECT
    stack_id, update_id, sequence, timestamp_ms, payload
FROM update_events;

DROP TABLE update_events;

DROP TABLE checkpoints;

DROP TABLE updates;

ALTER TABLE updates_new RENAME TO updates;

CREATE TABLE checkpoints (
    stack_id       TEXT    NOT NULL REFERENCES stacks (id) ON DELETE CASCADE,
    update_id      TEXT    NOT NULL,
    lease_digest   BLOB    NOT NULL,
    schema_version INTEGER NOT NULL,
    sequence       INTEGER NOT NULL,
    digest         BLOB    NOT NULL,
    invalid        INTEGER NOT NULL,
    features_json  TEXT    NOT NULL,
    deployment     BLOB    NOT NULL,
    saved_at_ms    INTEGER NOT NULL,
    PRIMARY KEY (stack_id, update_id),
    FOREIGN KEY (stack_id) REFERENCES stacks (id) ON DELETE CASCADE,
    FOREIGN KEY (stack_id, update_id) REFERENCES updates (stack_id, id) ON DELETE CASCADE
) STRICT;

INSERT INTO checkpoints (
    stack_id, update_id, lease_digest, schema_version, sequence,
    digest, invalid, features_json, deployment, saved_at_ms
) SELECT
    stack_id, update_id, lease_digest, schema_version, sequence,
    digest, invalid, features_json, deployment, saved_at_ms
FROM _checkpoints_data;

DROP TABLE _checkpoints_data;

CREATE TABLE update_events (
    stack_id     TEXT    NOT NULL,
    update_id    TEXT    NOT NULL,
    sequence     INTEGER NOT NULL,
    timestamp_ms INTEGER NOT NULL,
    payload      BLOB    NOT NULL,
    PRIMARY KEY (stack_id, update_id, sequence),
    FOREIGN KEY (stack_id, update_id) REFERENCES updates (stack_id, id) ON DELETE CASCADE
) STRICT;

INSERT INTO update_events (stack_id, update_id, sequence, timestamp_ms, payload)
SELECT stack_id, update_id, sequence, timestamp_ms, payload FROM _events_data;

DROP TABLE _events_data;

CREATE UNIQUE INDEX updates_one_running ON updates (stack_id)
WHERE status = 'running' AND dry_run = 0 AND kind <> 'preview';

CREATE INDEX updates_history ON updates (stack_id, version)
WHERE status <> 'not started' AND dry_run = 0 AND kind <> 'preview';
