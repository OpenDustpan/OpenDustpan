-- Jobs, change freezes and the state records that the job rules read belong
-- to a namespace and live in a backup. All rows hold the stable ID of the
-- namespace. The unit of a job is its stable unit ID: a rename of the unit
-- keeps its jobs, and a job outlives its unit, so there is no foreign key to
-- the inventory. body holds the JSON of the fields that no query reads.
-- seq orders the jobs of one namespace, newest first. It counts per namespace,
-- and the namespace row lock of a write serializes the count. current_operation is the durable
-- operation of the current phase, or empty before its dispatch.
-- current_changes is 1 when the current phase changes resources, state or the
-- code in git.
CREATE TABLE jobs (
    id                TEXT    NOT NULL PRIMARY KEY,
    namespace_id      TEXT    NOT NULL REFERENCES namespaces (id),
    unit_id           TEXT    NOT NULL,
    seq               INTEGER NOT NULL,
    state             TEXT    NOT NULL,
    wait              TEXT    NOT NULL,
    current_phase     INTEGER NOT NULL,
    current_changes   INTEGER NOT NULL CHECK (current_changes IN (0, 1)),
    current_operation TEXT    NOT NULL,
    idempotency_key   TEXT    NOT NULL,
    version           INTEGER NOT NULL,
    body              TEXT    NOT NULL,
    created_at_ms     INTEGER NOT NULL,
    updated_at_ms     INTEGER NOT NULL
) STRICT;

-- One change at a time on a unit: a second job that holds the unit for a
-- change fails on this index, in its own transaction.
CREATE UNIQUE INDEX jobs_one_change ON jobs (unit_id)
    WHERE current_changes = 1 AND state IN ('dispatched', 'running', 'canceling');

-- Two starts with one key in one namespace give one job.
CREATE UNIQUE INDEX jobs_key ON jobs (namespace_id, idempotency_key)
    WHERE idempotency_key <> '';

CREATE UNIQUE INDEX jobs_list ON jobs (namespace_id, seq);
CREATE INDEX jobs_unit ON jobs (unit_id, seq);

-- The open jobs of a namespace: the count of the limits, the orphan scan and
-- the reconcile read only these rows.
CREATE INDEX jobs_open ON jobs (namespace_id, state)
    WHERE state NOT IN ('succeeded', 'failed', 'canceled', 'rejected', 'expired');

CREATE TABLE job_freezes (
    id            TEXT    NOT NULL PRIMARY KEY,
    namespace_id  TEXT    NOT NULL REFERENCES namespaces (id),
    reason        TEXT    NOT NULL,
    starts_at_ms  INTEGER NOT NULL,
    ends_at_ms    INTEGER NOT NULL,
    created_by    TEXT    NOT NULL,
    ended_by      TEXT    NOT NULL
) STRICT;

CREATE INDEX job_freezes_namespace ON job_freezes (namespace_id, starts_at_ms);

-- The current state version of a unit, as the state write of its tool sets
-- it. A plan reads it at its claim, and an apply phase dispatches only while
-- it is the same.
CREATE TABLE unit_state_versions (
    unit_id       TEXT    NOT NULL PRIMARY KEY,
    namespace_id  TEXT    NOT NULL REFERENCES namespaces (id),
    version_id    TEXT    NOT NULL,
    updated_at_ms INTEGER NOT NULL
) STRICT;

-- The last final state version of each unit, also when no job waits. A merge
-- or a delivery that a job learns late still closes it.
CREATE TABLE unit_final_versions (
    unit_id       TEXT    NOT NULL PRIMARY KEY,
    namespace_id  TEXT    NOT NULL REFERENCES namespaces (id),
    version_id    TEXT    NOT NULL,
    commit_id     TEXT    NOT NULL,
    job_id        TEXT    NOT NULL,
    at_ms         INTEGER NOT NULL
) STRICT;
