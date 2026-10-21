-- One record for each item key of a history item: a stored plan or a version
-- of a job log, and later a state item. The wrapped key itself is a blob
-- outside SQL; this row names it and tells whether a redaction shredded it.
-- The record belongs to a namespace and lives in a backup. scope_id is the
-- stable ID of the unit, never its name. kind is the item kind of
-- secret.ItemKind and item is the ID that the tool or the job model gives
-- the item. shredded_at_ms is 0 while the key exists. A record never comes
-- back after a shred: a new item gets a new key.
CREATE TABLE item_keys (
    id             TEXT    NOT NULL PRIMARY KEY,
    namespace_id   TEXT    NOT NULL REFERENCES namespaces (id),
    scope_id       TEXT    NOT NULL,
    kind           TEXT    NOT NULL CHECK (kind IN ('state', 'plan', 'job-log')),
    item           TEXT    NOT NULL,
    wrapper_id     TEXT    NOT NULL,
    created_at_ms  INTEGER NOT NULL,
    shredded_at_ms INTEGER NOT NULL CHECK (shredded_at_ms >= 0)
) STRICT;

-- One key for each item.
CREATE UNIQUE INDEX item_keys_item ON item_keys (namespace_id, scope_id, kind, item);

-- The purge lists the records of one scope by ID.
CREATE INDEX item_keys_scope ON item_keys (namespace_id, scope_id, id);

-- The start-up check reads the wrapper IDs of the keys that exist.
CREATE INDEX item_keys_wrapper ON item_keys (wrapper_id) WHERE shredded_at_ms = 0;
