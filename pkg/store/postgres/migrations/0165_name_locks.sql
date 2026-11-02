-- The lockout by name of the password sign-in. Each row is the count of one
-- name and one sub-key in one scope: the failures in a row, the end of the
-- lock (0 for none), the level of the doubling, the time of the last failure
-- and of the end of the last lock, and the reset time of the rule that wrote
-- it. The name is a keyed digest of a user name or a principal ID, never a
-- user name. The server checks each value before it writes; the CHECK
-- constraints refuse a row that skips those checks.
CREATE TABLE name_locks (
    scope            text   NOT NULL CHECK (scope ~ '^[a-z]{1,32}$'),
    name             text   NOT NULL CHECK (length(name) BETWEEN 1 AND 256),
    sub              text   NOT NULL DEFAULT '' CHECK (length(sub) <= 256),
    failures         bigint NOT NULL DEFAULT 0 CHECK (failures >= 0),
    locked_until_ms  bigint NOT NULL DEFAULT 0 CHECK (locked_until_ms >= 0),
    level            bigint NOT NULL DEFAULT 0 CHECK (level BETWEEN 0 AND 64),
    last_failure_ms  bigint NOT NULL DEFAULT 0 CHECK (last_failure_ms >= 0),
    last_lock_end_ms bigint NOT NULL DEFAULT 0 CHECK (last_lock_end_ms >= 0),
    reset_after_ms   bigint NOT NULL DEFAULT 0 CHECK (reset_after_ms >= 0),
    PRIMARY KEY (scope, name, sub)
);

-- The key of the digests of the names of name_locks: 32 random bytes that the
-- first instance writes. Every instance on the database reads the same key,
-- so every instance computes the same digest of a user name.
CREATE TABLE name_lock_key (
    id  integer NOT NULL PRIMARY KEY CHECK (id = 1),
    key bytea   NOT NULL CHECK (length(key) = 32)
);
