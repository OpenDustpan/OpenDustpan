-- Runner pools, runners and join tokens belong to a namespace and live in a
-- backup. All rows hold the stable ID of the namespace. A zero in a *_ms
-- column that can be empty means "not set". lease_ms and concurrency are 0
-- when the pool takes the default. isolation and labels hold JSON.
CREATE TABLE runner_pools (
    id             text    NOT NULL PRIMARY KEY,
    namespace_id   text    NOT NULL,
    name           text    NOT NULL,
    purpose        text    NOT NULL,
    lease_ms       bigint NOT NULL,
    concurrency    bigint NOT NULL,
    isolation      text    NOT NULL,
    git_mode       text    NOT NULL,
    git_helper     text    NOT NULL,
    created_at_ms  bigint NOT NULL,
    updated_at_ms  bigint NOT NULL,
    UNIQUE (namespace_id, name),
    CHECK (purpose IN ('jobs', 'sweeps'))
);

-- The store keeps the SHA-256 of the secret, never the secret.
CREATE TABLE runner_join_tokens (
    id            text    NOT NULL PRIMARY KEY,
    namespace_id  text    NOT NULL,
    pool_id       text    NOT NULL REFERENCES runner_pools (id),
    sha256        text    NOT NULL UNIQUE,
    created_by    text    NOT NULL,
    created_at_ms bigint NOT NULL,
    expires_at_ms bigint NOT NULL,
    used_at_ms    bigint NOT NULL,
    used_by       text    NOT NULL,
    revoked_at_ms bigint NOT NULL
);

CREATE INDEX runner_join_tokens_pool ON runner_join_tokens (pool_id, created_at_ms, id);

CREATE TABLE runners (
    id                     text    NOT NULL PRIMARY KEY,
    namespace_id           text    NOT NULL,
    pool_id                text    NOT NULL REFERENCES runner_pools (id),
    name                   text    NOT NULL,
    kind                   text    NOT NULL,
    isolation              text    NOT NULL,
    labels                 text    NOT NULL,
    release                text    NOT NULL,
    key_public             bytea    NOT NULL,
    key_created_at_ms      bigint NOT NULL,
    previous_key_public    bytea    NOT NULL,
    previous_key_created_at_ms bigint NOT NULL,
    rotated_at_ms          bigint NOT NULL,
    joined_with            text    NOT NULL,
    draining               boolean NOT NULL,
    retired_at_ms          bigint NOT NULL,
    last_seen_at_ms        bigint NOT NULL,
    joined_at_ms           bigint NOT NULL,
    CHECK (kind IN ('kubernetes', 'machine'))
);

-- A runner name is unique in its pool while the runner is not retired.
CREATE UNIQUE INDEX runners_name ON runners (pool_id, name) WHERE retired_at_ms = 0;

-- Control data: a backup does not hold the tables below. A claim time is
-- recorded once for each claim, a handoff token and a job session belong to
-- one claim, and a nonce lives until its expiry. The claim of a durable
-- operation has no foreign key here, because the operation rows are control
-- data too.
CREATE TABLE runner_claims (
    operation_id  text    NOT NULL,
    token         bigint NOT NULL,
    claimed_at_ms bigint NOT NULL,
    PRIMARY KEY (operation_id, token)
);

CREATE TABLE runner_handoff_tokens (
    sha256        text    NOT NULL PRIMARY KEY,
    operation_id  text    NOT NULL,
    token         bigint NOT NULL,
    runner_id     text    NOT NULL,
    issued_at_ms  bigint NOT NULL,
    expires_at_ms bigint NOT NULL,
    used_at_ms    bigint NOT NULL,
    UNIQUE (operation_id, token)
);

CREATE TABLE runner_sessions (
    id                text    NOT NULL PRIMARY KEY,
    operation_id      text    NOT NULL,
    token             bigint NOT NULL,
    runner_id         text    NOT NULL,
    key_public        bytea    NOT NULL,
    key_created_at_ms bigint NOT NULL,
    created_at_ms     bigint NOT NULL,
    UNIQUE (operation_id, token)
);

CREATE TABLE runner_nonces (
    subject       text    NOT NULL,
    nonce         text    NOT NULL,
    expires_at_ms bigint NOT NULL,
    PRIMARY KEY (subject, nonce)
);

CREATE INDEX runner_nonces_expiry ON runner_nonces (expires_at_ms);
