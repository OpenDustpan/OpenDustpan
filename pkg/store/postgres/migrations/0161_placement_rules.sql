-- Placement rules. A rule of a namespace matches a new unit of one tool by a
-- project pattern and a stack-name pattern, and names the collection of the
-- unit. The rules of a namespace are an ordered list, and the first rule that
-- matches wins. position runs from 1 with no gap: every write renumbers the
-- list in its own transaction, so position has no unique key (a unique key
-- would refuse the steps of a renumber). collection_key is the lower-case
-- name of the collection, as in collection_members. The store deletes the
-- rules of a collection in the transaction that deletes the collection.
CREATE TABLE placement_rules (
    id             text    NOT NULL PRIMARY KEY,
    namespace      text    NOT NULL REFERENCES namespaces (name),
    position       integer NOT NULL CHECK (position >= 1),
    tool           text    NOT NULL,
    project        text    NOT NULL,
    stack          text    NOT NULL,
    collection_key text    NOT NULL,
    created_by     text    NOT NULL,
    created_at_ms  bigint  NOT NULL,
    FOREIGN KEY (namespace, collection_key) REFERENCES collections (namespace, name_key)
);

CREATE INDEX placement_rules_order ON placement_rules (namespace, position);
