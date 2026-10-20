-- Manual locks: a lock that a person puts on one unit or on one collection,
-- with a reason. A tool lock stays in the tables of the tool. One row holds
-- the lock of one object. object_id is the stable ID of the object: the unit
-- ID of the inventory for a unit, so that a rename keeps the lock, and the ID
-- of the collection for a collection, so that a collection created again under
-- the same name does not inherit the lock. A lock on a collection covers each
-- unit that is in the collection now: a read joins the membership, so a unit
-- that joins is covered and a unit that leaves is not.
CREATE TABLE manual_locks (
    kind      text   NOT NULL CHECK (kind IN ('unit', 'collection')),
    object_id text   NOT NULL,
    namespace text   NOT NULL REFERENCES namespaces (name),
    holder    text   NOT NULL CHECK (holder <> ''),
    reason    text   NOT NULL CHECK (reason <> '' AND char_length(reason) <= 500),
    since_ms  bigint NOT NULL,
    PRIMARY KEY (kind, object_id)
);

CREATE INDEX manual_locks_namespace ON manual_locks (namespace);
