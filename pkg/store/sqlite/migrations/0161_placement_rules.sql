-- Placement rules. A rule of a namespace matches a new unit of one tool by a
-- project pattern and a stack-name pattern, and names the collection of the
-- unit. The rules of a namespace are an ordered list, and the first rule that
-- matches wins. position runs from 1 with no gap: every write renumbers the
-- list in its own transaction, so position has no unique key (a unique key
-- would refuse the steps of a renumber). collection_key is the lower-case
-- name of the collection, as in collection_members. The store deletes the
-- rules of a collection in the transaction that deletes the collection.
CREATE TABLE placement_rules (
    id             TEXT    NOT NULL PRIMARY KEY,
    namespace      TEXT    NOT NULL REFERENCES namespaces (name),
    position       INTEGER NOT NULL CHECK (position >= 1),
    tool           TEXT    NOT NULL,
    project        TEXT    NOT NULL,
    stack          TEXT    NOT NULL,
    collection_key TEXT    NOT NULL,
    created_by     TEXT    NOT NULL,
    created_at_ms  INTEGER NOT NULL,
    FOREIGN KEY (namespace, collection_key) REFERENCES collections (namespace, name_key)
) STRICT;

CREATE INDEX placement_rules_order ON placement_rules (namespace, position);
