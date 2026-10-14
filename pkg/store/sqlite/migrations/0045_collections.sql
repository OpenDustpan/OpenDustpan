-- Collections: a named set of units in one namespace (ADR 51). A collection is
-- soft: it has no fence of its own, and its writes carry the fence of its
-- namespace. The ID is stable and random, so a deleted and re-created name
-- does not inherit the grants of the old collection. The key is the name in
-- lower case, so two names that differ only in case are one collection. There
-- is no default collection: a namespace has only the collections that people
-- make.
CREATE TABLE collections (
    id            TEXT    NOT NULL UNIQUE,
    namespace     TEXT    NOT NULL REFERENCES namespaces (name),
    name_key      TEXT    NOT NULL,
    name          TEXT    NOT NULL,
    created_at_ms INTEGER NOT NULL,
    PRIMARY KEY (namespace, name_key)
) STRICT;

-- The collection of each unit. The unit ID is the primary key, so a unit is
-- in no more than one collection (ADR 51, decision 4). The unit ID stays with
-- the unit through a rename (ADR 19), so the collection of a renamed unit
-- stays the same. The collection of a unit is read by a join on unit_id, and
-- no copy of it is kept in the inventory. A unit with no row has no
-- collection. Refusing a write to such a unit is not in this migration: PR 2
-- adds that refusal (ADR 51, decision 5).
CREATE TABLE collection_members (
    unit_id        TEXT    NOT NULL PRIMARY KEY,
    namespace      TEXT    NOT NULL,
    collection_key TEXT    NOT NULL,
    assigned_at_ms INTEGER NOT NULL,
    FOREIGN KEY (namespace, collection_key) REFERENCES collections (namespace, name_key)
) STRICT;

CREATE INDEX collection_members_collection ON collection_members (namespace, collection_key);
