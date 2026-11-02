-- Manual locks: a lock that a person puts on one unit or on one collection,
-- with a reason. A tool lock stays in the tables of the tool. One row holds
-- the lock of one object. object_id is the stable ID of the object: the unit
-- ID of the inventory for a unit, so that a rename keeps the lock, and the ID
-- of the collection for a collection, so that a collection created again under
-- the same name does not inherit the lock. A lock on a collection covers each
-- unit that is in the collection now: a read joins the membership, so a unit
-- that joins is covered and a unit that leaves is not.
CREATE TABLE manual_locks (
    kind      TEXT    NOT NULL CHECK (kind IN ('unit', 'collection')),
    object_id TEXT    NOT NULL,
    namespace TEXT    NOT NULL REFERENCES namespaces (name),
    holder    TEXT    NOT NULL CHECK (holder <> ''),
    reason    TEXT    NOT NULL CHECK (reason <> '' AND length(reason) <= 500),
    since_ms  INTEGER NOT NULL,
    PRIMARY KEY (kind, object_id)
) STRICT;

CREATE INDEX manual_locks_namespace ON manual_locks (namespace);
