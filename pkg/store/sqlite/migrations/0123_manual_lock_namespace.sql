-- A manual lock on a whole namespace. The kind namespace joins unit and
-- collection, and object_id is the ID of the namespace, so that a namespace
-- gets no lock from an older namespace of the same name. SQLite cannot change
-- a CHECK constraint, so the table is rebuilt with the same columns and rows.
CREATE TABLE manual_locks_new (
    kind      TEXT    NOT NULL CHECK (kind IN ('unit', 'collection', 'namespace')),
    object_id TEXT    NOT NULL,
    namespace TEXT    NOT NULL REFERENCES namespaces (name),
    holder    TEXT    NOT NULL CHECK (holder <> ''),
    reason    TEXT    NOT NULL CHECK (reason <> '' AND length(reason) <= 500),
    since_ms  INTEGER NOT NULL,
    PRIMARY KEY (kind, object_id)
) STRICT;

INSERT INTO manual_locks_new (kind, object_id, namespace, holder, reason, since_ms)
SELECT kind, object_id, namespace, holder, reason, since_ms FROM manual_locks;

DROP TABLE manual_locks;

ALTER TABLE manual_locks_new RENAME TO manual_locks;

CREATE INDEX manual_locks_namespace ON manual_locks (namespace);
