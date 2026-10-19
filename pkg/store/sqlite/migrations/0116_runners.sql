-- Runner pools, runners and join tokens belong to a namespace and live in a
-- backup. All rows hold the stable ID of the namespace. A zero in a *_ms
-- column that can be empty means "not set". lease_ms and concurrency are 0
-- when the pool takes the default. isolation and labels hold JSON.
CREATE TABLE runner_pools (
    id             TEXT    NOT NULL PRIMARY KEY,
    namespace_id   TEXT    NOT NULL,
    name           TEXT    NOT NULL,
    purpose        TEXT    NOT NULL,
    lease_ms       INTEGER NOT NULL,
    concurrency    INTEGER NOT NULL,
    isolation      TEXT    NOT NULL,
    git_mode       TEXT    NOT NULL,
    git_helper     TEXT    NOT NULL,
    created_at_ms  INTEGER NOT NULL,
    updated_at_ms  INTEGER NOT NULL,
    UNIQUE (namespace_id, name),
    CHECK (purpose IN ('jobs', 'sweeps'))
) STRICT;

-- The store keeps the SHA-256 of the secret, never the secret.
CREATE TABLE runner_join_tokens (
    id            TEXT    NOT NULL PRIMARY KEY,
    namespace_id  TEXT    NOT NULL,
    pool_id       TEXT    NOT NULL REFERENCES runner_pools (id),
    sha256        TEXT    NOT NULL UNIQUE,
    created_by    TEXT    NOT NULL,
    created_at_ms INTEGER NOT NULL,
    expires_at_ms INTEGER NOT NULL,
    used_at_ms    INTEGER NOT NULL,
    used_by       TEXT    NOT NULL,
    revoked_at_ms INTEGER NOT NULL
) STRICT;

CREATE INDEX runner_join_tokens_pool ON runner_join_tokens (pool_id, created_at_ms, id);

CREATE TABLE runners (
    id                     TEXT    NOT NULL PRIMARY KEY,
    namespace_id           TEXT    NOT NULL,
    pool_id                TEXT    NOT NULL REFERENCES runner_pools (id),
    name                   TEXT    NOT NULL,
    kind                   TEXT    NOT NULL,
    isolation              TEXT    NOT NULL,
    labels                 TEXT    NOT NULL,
    release                TEXT    NOT NULL,
    key_public             BLOB    NOT NULL,
    key_created_at_ms      INTEGER NOT NULL,
    previous_key_public    BLOB    NOT NULL,
    previous_key_created_at_ms INTEGER NOT NULL,
    rotated_at_ms          INTEGER NOT NULL,
    joined_with            TEXT    NOT NULL,
    draining               INTEGER NOT NULL,
    retired_at_ms          INTEGER NOT NULL,
    last_seen_at_ms        INTEGER NOT NULL,
    joined_at_ms           INTEGER NOT NULL,
    CHECK (kind IN ('kubernetes', 'machine'))
) STRICT;

-- A runner name is unique in its pool while the runner is not retired.
CREATE UNIQUE INDEX runners_name ON runners (pool_id, name) WHERE retired_at_ms = 0;

-- Control data: a backup does not hold the tables below. A claim time is
-- recorded once for each claim, a handoff token and a job session belong to
-- one claim, and a nonce lives until its expiry. The claim of a durable
-- operation has no foreign key here, because the operation rows are control
-- data too.
CREATE TABLE runner_claims (
    operation_id  TEXT    NOT NULL,
    token         INTEGER NOT NULL,
    claimed_at_ms INTEGER NOT NULL,
    PRIMARY KEY (operation_id, token)
) STRICT;

CREATE TABLE runner_handoff_tokens (
    sha256        TEXT    NOT NULL PRIMARY KEY,
    operation_id  TEXT    NOT NULL,
    token         INTEGER NOT NULL,
    runner_id     TEXT    NOT NULL,
    issued_at_ms  INTEGER NOT NULL,
    expires_at_ms INTEGER NOT NULL,
    used_at_ms    INTEGER NOT NULL,
    UNIQUE (operation_id, token)
) STRICT;

CREATE TABLE runner_sessions (
    id                TEXT    NOT NULL PRIMARY KEY,
    operation_id      TEXT    NOT NULL,
    token             INTEGER NOT NULL,
    runner_id         TEXT    NOT NULL,
    key_public        BLOB    NOT NULL,
    key_created_at_ms INTEGER NOT NULL,
    created_at_ms     INTEGER NOT NULL,
    UNIQUE (operation_id, token)
) STRICT;

CREATE TABLE runner_nonces (
    subject       TEXT    NOT NULL,
    nonce         TEXT    NOT NULL,
    expires_at_ms INTEGER NOT NULL,
    PRIMARY KEY (subject, nonce)
) STRICT;

CREATE INDEX runner_nonces_expiry ON runner_nonces (expires_at_ms);
