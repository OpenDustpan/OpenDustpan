-- Audit entries of the installation (ADR 60, decisions 6 and 7). An action
-- that is not in a namespace, for example a change of a principal or a
-- group, writes to the scope @server, with its own sequence and chain. The
-- namespace column holds the scope, so it can no longer name a row of
-- namespaces: this migration rebuilds the table without that foreign key.
-- SQLite cannot drop a foreign key in place. The rows, the columns and the
-- index stay as they were.
CREATE TABLE audit_entries_scoped (
    namespace    TEXT    NOT NULL,
    sequence     INTEGER NOT NULL,
    subject      TEXT    NOT NULL,
    idp_subject  TEXT    NOT NULL DEFAULT '',
    token_id     TEXT    NOT NULL DEFAULT '',
    action       TEXT    NOT NULL,
    object       TEXT    NOT NULL,
    version      TEXT    NOT NULL DEFAULT '',
    value_path   TEXT    NOT NULL DEFAULT '',
    at_ms        INTEGER NOT NULL,
    source_ip    TEXT    NOT NULL DEFAULT '',
    user_agent   TEXT    NOT NULL DEFAULT '',
    channel      TEXT    NOT NULL,
    kind         TEXT    NOT NULL,
    outcome      TEXT    NOT NULL,
    request_id   TEXT    NOT NULL DEFAULT '',
    prev_hash    TEXT    NOT NULL DEFAULT '',
    hash         TEXT    NOT NULL,
    value_count  INTEGER,
    PRIMARY KEY (namespace, sequence)
) STRICT;

INSERT INTO audit_entries_scoped (
    namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
    at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count
)
SELECT
    namespace, sequence, subject, idp_subject, token_id, action, object, version, value_path,
    at_ms, source_ip, user_agent, channel, kind, outcome, request_id, prev_hash, hash, value_count
FROM audit_entries;

DROP TABLE audit_entries;

ALTER TABLE audit_entries_scoped RENAME TO audit_entries;

CREATE INDEX audit_entries_ns_time ON audit_entries (namespace, at_ms DESC, sequence DESC);
