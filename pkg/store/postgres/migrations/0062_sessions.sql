-- Console sessions and sign-in requests. A session belongs to one person
-- principal. Its ID is opaque, with the prefix ses-. The table keeps the
-- SHA-256 digest of the secret in lower-case hex, never the secret.
-- provider names the sign-in method. auth_time_ms is the time of the
-- authentication at the identity provider. last_seen_at_ms is the time of
-- the last recorded use. ended_at_ms and end_reason are set by a sign-out
-- or by an administrator; the idle limit and the maximum length end a
-- session without a write. source_ip and user_agent describe the browser
-- at the sign-in.
--
-- A sign-in request holds the state of one sign-in between its start and
-- its callback, keyed by the digest of the state. A callback deletes it,
-- so it works once. It expires after 10 minutes.
--
-- last_sign_in_at_ms on a principal is the time of its last sign-in. The
-- inactivity disable counts it as activity. It is NULL until the first
-- sign-in.
--
-- The rows carry no fence: they describe the installation, not one
-- namespace. The ID index uses the C collation, so a keyset read compares
-- bytes as SQLite does.
CREATE TABLE sessions (
    id              TEXT   NOT NULL PRIMARY KEY,
    principal_id    TEXT   NOT NULL REFERENCES principals (id),
    digest          TEXT   NOT NULL,
    provider        TEXT   NOT NULL,
    created_at_ms   BIGINT NOT NULL,
    last_seen_at_ms BIGINT NOT NULL,
    auth_time_ms    BIGINT NOT NULL,
    ended_at_ms     BIGINT,
    end_reason      TEXT   NOT NULL DEFAULT '',
    source_ip       TEXT   NOT NULL DEFAULT '',
    user_agent      TEXT   NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX sessions_digest ON sessions (digest);

CREATE INDEX sessions_principal ON sessions (principal_id, created_at_ms DESC, id COLLATE "C" DESC);

CREATE TABLE sign_in_requests (
    digest        TEXT   NOT NULL PRIMARY KEY,
    provider      TEXT   NOT NULL,
    nonce         TEXT   NOT NULL,
    verifier      TEXT   NOT NULL,
    return_path   TEXT   NOT NULL,
    created_at_ms BIGINT NOT NULL,
    expires_at_ms BIGINT NOT NULL
);

CREATE INDEX sign_in_requests_expiry ON sign_in_requests (expires_at_ms);

ALTER TABLE principals ADD COLUMN last_sign_in_at_ms BIGINT;
