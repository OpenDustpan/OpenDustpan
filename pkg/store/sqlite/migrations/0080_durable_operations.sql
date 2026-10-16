-- Durable operations: background work that survives the stop of the instance
-- that runs it. Several instances share these rows and coordinate only
-- through them.
--
-- durable_operations holds one row for each operation. start_key makes two
-- starts with one key give one operation. One instance at a time holds a
-- claim on a running operation: claim_owner names it, and
-- claim_expires_at_ms is its expiry on the database clock. Both are NULL
-- when no instance holds the operation. claim_token grows on each claim and
-- never goes back, also when a claim ends, so a holder that lost its claim
-- cannot write. attempt counts the claims, expiries counts the claims that
-- took over an expired claim, and failures counts the failed attempts. version is the version of the operation
-- that Start records. not_before_ms is the earliest time of the next claim
-- of a pending operation.
--
-- durable_steps holds the recorded steps of each operation. seq keeps the
-- order of the records.
CREATE TABLE durable_operations (
    id                  TEXT    NOT NULL PRIMARY KEY,
    start_key           TEXT    NOT NULL UNIQUE,
    kind                TEXT    NOT NULL,
    input               BLOB    NOT NULL,
    version             INTEGER NOT NULL,
    state               TEXT    NOT NULL
        CHECK (state IN ('pending', 'running', 'succeeded', 'failed', 'canceled')),
    attempt             INTEGER NOT NULL,
    expiries            INTEGER NOT NULL DEFAULT 0,
    failures            INTEGER NOT NULL DEFAULT 0,
    last_error          TEXT    NOT NULL,
    claim_owner         TEXT,
    claim_token         INTEGER NOT NULL,
    claim_expires_at_ms INTEGER,
    cancel_requested    INTEGER NOT NULL,
    not_before_ms       INTEGER NOT NULL,
    created_at_ms       INTEGER NOT NULL,
    updated_at_ms       INTEGER NOT NULL
) STRICT;

-- A claim reads the operations that can run, oldest first.
CREATE INDEX durable_operations_open ON durable_operations (created_at_ms, id)
    WHERE state IN ('pending', 'running');

CREATE INDEX durable_operations_created ON durable_operations (created_at_ms, id);

CREATE TABLE durable_steps (
    operation_id TEXT    NOT NULL REFERENCES durable_operations (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    seq          INTEGER NOT NULL,
    result       BLOB    NOT NULL,
    done_at_ms   INTEGER NOT NULL,
    PRIMARY KEY (operation_id, name),
    UNIQUE (operation_id, seq)
) STRICT;
