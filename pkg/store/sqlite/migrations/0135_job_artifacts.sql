-- The lookup rows of the plans and the log versions of jobs, and the rows of
-- log chunks. They belong to a namespace and live in a backup. All rows hold
-- the stable ID of the namespace. The content of a plan or of a chunk is a
-- sealed blob outside SQL.
--
-- job_plans holds one row for each stored plan, also one that a replan cleared
-- from its phase, because the plan stays a history item until its retention
-- ends. body is the JSON of the plan: the parts with their hashes, the item
-- key ID, the state version and the times. job_id is not a foreign key,
-- because the rows of a namespace restore in a fixed order.
CREATE TABLE job_plans (
    id            TEXT    NOT NULL PRIMARY KEY,
    namespace_id  TEXT    NOT NULL REFERENCES namespaces (id),
    job_id        TEXT    NOT NULL,
    body          TEXT    NOT NULL,
    created_at_ms INTEGER NOT NULL
) STRICT;

CREATE INDEX job_plans_job ON job_plans (job_id);

-- job_logs holds one row for each log version. The version itself (its chunk
-- count, its hash, its times) is in the step record of the job. A row names the
-- step record: the phase, the step name and the token of the claim that ran
-- the step.
CREATE TABLE job_logs (
    id            TEXT    NOT NULL PRIMARY KEY,
    namespace_id  TEXT    NOT NULL REFERENCES namespaces (id),
    job_id        TEXT    NOT NULL,
    phase         INTEGER NOT NULL CHECK (phase >= 0),
    step          TEXT    NOT NULL,
    token         INTEGER NOT NULL,
    created_at_ms INTEGER NOT NULL
) STRICT;

CREATE INDEX job_logs_job ON job_logs (job_id);

-- job_log_chunks holds the number, the SHA-256 and the size of each chunk of a
-- log version. A row exists when a writer reserved the chunk. confirmed is 1
-- when the blob of the chunk is stored and the chunk counts in the log.
CREATE TABLE job_log_chunks (
    log_id       TEXT    NOT NULL,
    seq          INTEGER NOT NULL CHECK (seq >= 0),
    namespace_id TEXT    NOT NULL REFERENCES namespaces (id),
    sha256       TEXT    NOT NULL,
    size         INTEGER NOT NULL CHECK (size >= 0),
    confirmed    INTEGER NOT NULL CHECK (confirmed IN (0, 1)),
    PRIMARY KEY (log_id, seq)
) STRICT;
