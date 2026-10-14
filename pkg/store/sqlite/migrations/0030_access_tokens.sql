-- Access tokens of the principals (ADR 53). A token belongs to one principal.
-- Its ID is opaque, with the prefix tok-. The table keeps the SHA-256 digest
-- of the secret in lower-case hex, never the secret. kind is personal or
-- service. expires_at_ms is the end of the token: a rotation moves it to the
-- end time of the old token and sets replaced_by. last_used_at_ms is NULL
-- until a request uses the token. revoked_at_ms is set by a revoke.
--
-- Nothing is erased: a revoked or expired token keeps its row for the
-- history. The rows carry no fence: they describe the installation, not one
-- namespace.
CREATE TABLE access_tokens (
    id              TEXT    NOT NULL PRIMARY KEY,
    principal_id    TEXT    NOT NULL REFERENCES principals (id),
    name            TEXT    NOT NULL,
    kind            TEXT    NOT NULL,
    digest          TEXT    NOT NULL,
    created_at_ms   INTEGER NOT NULL,
    expires_at_ms   INTEGER NOT NULL,
    last_used_at_ms INTEGER,
    revoked_at_ms   INTEGER,
    replaced_by     TEXT
) STRICT;

CREATE UNIQUE INDEX access_tokens_digest ON access_tokens (digest);

-- The lists read the tokens newest first, of every principal or of one.
CREATE INDEX access_tokens_created ON access_tokens (created_at_ms DESC, id DESC);

CREATE INDEX access_tokens_principal ON access_tokens (principal_id, created_at_ms DESC, id DESC);
