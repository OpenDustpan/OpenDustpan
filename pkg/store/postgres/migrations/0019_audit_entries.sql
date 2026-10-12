-- Audit entries: the append-only audit log of each namespace (ADR 41
-- decisions 5 and 13). Each row records one action: who acted (subject,
-- identity provider subject, access token ID), what happened (action,
-- object, version, value path), when it happened (UTC, Unix milliseconds),
-- where it came from (source IP, user agent, channel) and how it ended
-- (outcome, request ID). Kind tells a reveal apart from a tool operation.
-- Sequence numbers the entries of one namespace from 1. PrevHash holds the
-- hash of the previous entry of the namespace and is empty for the first
-- entry; hash holds the SHA-256 of the entry (see audit.ComputeHash). No
-- code updates or deletes a row: the check reads the chain and reports the
-- first entry that does not match.
CREATE TABLE audit_entries (
    namespace    TEXT   NOT NULL REFERENCES namespaces (name),
    sequence     BIGINT NOT NULL,
    subject      TEXT   NOT NULL,
    idp_subject  TEXT   NOT NULL DEFAULT '',
    token_id     TEXT   NOT NULL DEFAULT '',
    action       TEXT   NOT NULL,
    object       TEXT   NOT NULL,
    version      TEXT   NOT NULL DEFAULT '',
    value_path   TEXT   NOT NULL DEFAULT '',
    at_ms        BIGINT NOT NULL,
    source_ip    TEXT   NOT NULL DEFAULT '',
    user_agent   TEXT   NOT NULL DEFAULT '',
    channel      TEXT   NOT NULL,
    kind         TEXT   NOT NULL,
    outcome      TEXT   NOT NULL,
    request_id   TEXT   NOT NULL DEFAULT '',
    prev_hash    TEXT   NOT NULL DEFAULT '',
    hash         TEXT   NOT NULL,
    PRIMARY KEY (namespace, sequence)
);

CREATE INDEX audit_entries_ns_time ON audit_entries (namespace, at_ms DESC, sequence DESC);
