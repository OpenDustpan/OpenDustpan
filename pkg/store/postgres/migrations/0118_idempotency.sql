-- Replay records of the Idempotency-Key header. A record is scoped by the
-- caller, the method, the path and the key. status is 0 while the first
-- request runs (a pending record) and the HTTP status of the first answer
-- after it. fingerprint is the HMAC-SHA-256 of the request, never the request.
-- A backup leaves both tables out and a restore leaves them empty, so a
-- replay never answers for state that the restore removed. The server key
-- is made again at the next request.
-- The server key is sealed: wrapped_key holds the HMAC key wrapped by the
-- key-encryption key, and wrapper_id names the wrapper that sealed it. When
-- the key-encryption key cannot open the row, the next request replaces the
-- row and deletes every record in one transaction.
CREATE TABLE idempotency_records (
    principal text NOT NULL,
    method text NOT NULL,
    path text NOT NULL,
    idem_key text NOT NULL,
    fingerprint bytea NOT NULL,
    token text NOT NULL,
    status bigint NOT NULL,
    headers text NOT NULL,
    body bytea NOT NULL,
    created_at_ms bigint NOT NULL,
    PRIMARY KEY (principal, method, path, idem_key),
    CHECK (status = 0 OR status BETWEEN 200 AND 499)
);

CREATE INDEX idempotency_records_created ON idempotency_records (created_at_ms);

-- The sealed server key of the fingerprints: one row.
CREATE TABLE idempotency_server_key (
    id bigint NOT NULL PRIMARY KEY,
    wrapped_key bytea NOT NULL,
    wrapper_id text NOT NULL,
    CHECK (id = 1)
);
