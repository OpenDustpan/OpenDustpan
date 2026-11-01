-- Local users. A local user is one person principal with a user name and,
-- when the person has one, a password hash in the PHC string form of
-- Argon2id. A user with no password has an empty hash and a zero set time.
-- The user name is in the stored form (lowercase) and has 1 to 64
-- characters of a-z, 0-9, '.', '_' and '-', with a letter or a digit first.
-- The store checks each value before it writes; the CHECK constraints refuse
-- a row that skips those checks.
CREATE TABLE local_users (
    id                 text    NOT NULL PRIMARY KEY,
    principal_id       text    NOT NULL UNIQUE REFERENCES principals (id),
    user_name          text    NOT NULL UNIQUE CHECK (user_name ~ '^[a-z0-9][a-z0-9._-]{0,63}$'),
    password_hash      text    NOT NULL DEFAULT '' CHECK (
        password_hash = ''
        OR (length(password_hash) <= 256 AND left(password_hash, 15) = '$argon2id$v=19$')
    ),
    password_set_at_ms bigint  NOT NULL DEFAULT 0,
    change_required    boolean NOT NULL DEFAULT false,
    demo               boolean NOT NULL DEFAULT false,
    created_by         text    NOT NULL,
    created_at_ms      bigint  NOT NULL,
    UNIQUE (id, principal_id),
    CHECK (password_hash <> '' OR (NOT change_required AND password_set_at_ms = 0))
);

-- Reset codes and set-up codes of local users. The table keeps the SHA-256
-- digest of each code in hexadecimal, never the code. A user has at most one
-- code: a new code replaces the earlier one. principal_id repeats the
-- principal of the user, and the composite foreign key keeps the two equal,
-- so the strip of dev data finds the codes of a dev principal by the column.
-- A backup leaves the codes out, and a restore leaves the table empty.
CREATE TABLE local_user_resets (
    id                   text    NOT NULL PRIMARY KEY,
    user_id              text    NOT NULL UNIQUE,
    principal_id         text    NOT NULL,
    digest               text    NOT NULL UNIQUE CHECK (digest ~ '^[0-9a-f]{64}$'),
    purpose              text    NOT NULL CHECK (purpose IN ('set-up', 'reset')),
    created_by           text    NOT NULL,
    reason               text    NOT NULL DEFAULT '',
    revoke_access_tokens boolean NOT NULL DEFAULT false,
    created_at_ms        bigint  NOT NULL,
    expires_at_ms        bigint  NOT NULL CHECK (expires_at_ms > created_at_ms),
    FOREIGN KEY (user_id, principal_id) REFERENCES local_users (id, principal_id) ON DELETE CASCADE
);

CREATE INDEX local_user_resets_expiry ON local_user_resets (expires_at_ms);

-- The session generation of each principal. A console session carries the
-- generation that its issue read, and works only while the generation is the
-- stored one. A password set, change or reset adds 1 in its own transaction,
-- so each session of the person ends.
ALTER TABLE principals ADD COLUMN session_generation bigint NOT NULL DEFAULT 1 CHECK (session_generation >= 1);
