-- Jobs, change freezes and the state records that the job rules read belong
-- to a namespace and live in a backup. All rows hold the stable ID of the
-- namespace. The unit of a job is its stable unit ID: a rename of the unit
-- keeps its jobs, and a job outlives its unit, so there is no foreign key to
-- the inventory. body holds the JSON of the fields that no query reads.
-- seq orders the jobs, newest first. current_operation is the durable
-- operation of the current phase, or empty before its dispatch.
-- current_changes is true when the current phase changes resources, state or the
-- code in git.
CREATE TABLE jobs (
    id                text    NOT NULL PRIMARY KEY,
    namespace_id      text    NOT NULL REFERENCES namespaces (id),
    unit_id           text    NOT NULL,
    seq               bigint NOT NULL,
    state             text    NOT NULL,
    wait              text    NOT NULL,
    current_phase     bigint NOT NULL,
    current_changes   boolean NOT NULL,
    current_operation text    NOT NULL,
    idempotency_key   text    NOT NULL,
    version           bigint NOT NULL,
    body              text    NOT NULL,
    created_at_ms     bigint NOT NULL,
    updated_at_ms     bigint NOT NULL
);

-- One change at a time on a unit: a second job that holds the unit for a
-- change fails on this index, in its own transaction.
CREATE UNIQUE INDEX jobs_one_change ON jobs (unit_id)
    WHERE current_changes AND state IN ('dispatched', 'running', 'canceling');

-- Two starts with one key in one namespace give one job.
CREATE UNIQUE INDEX jobs_key ON jobs (namespace_id, idempotency_key)
    WHERE idempotency_key <> '';

CREATE INDEX jobs_list ON jobs (namespace_id, seq);
CREATE INDEX jobs_unit ON jobs (unit_id, seq);

CREATE TABLE job_freezes (
    id            text    NOT NULL PRIMARY KEY,
    namespace_id  text    NOT NULL REFERENCES namespaces (id),
    reason        text    NOT NULL,
    starts_at_ms  bigint NOT NULL,
    ends_at_ms    bigint NOT NULL,
    created_by    text    NOT NULL,
    ended_by      text    NOT NULL
);

CREATE INDEX job_freezes_namespace ON job_freezes (namespace_id, starts_at_ms);

-- The current state version of a unit, as the state write of its tool sets
-- it. A plan reads it at its claim, and an apply phase dispatches only while
-- it is the same.
CREATE TABLE unit_state_versions (
    unit_id       text    NOT NULL PRIMARY KEY,
    namespace_id  text    NOT NULL REFERENCES namespaces (id),
    version_id    text    NOT NULL,
    updated_at_ms bigint NOT NULL
);

-- The last final state version of each unit, also when no job waits. A merge
-- or a delivery that a job learns late still closes it.
CREATE TABLE unit_final_versions (
    unit_id       text    NOT NULL PRIMARY KEY,
    namespace_id  text    NOT NULL REFERENCES namespaces (id),
    version_id    text    NOT NULL,
    commit_id     text    NOT NULL,
    job_id        text    NOT NULL,
    at_ms         bigint NOT NULL
);
