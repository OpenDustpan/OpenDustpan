-- Saved views belong to a namespace and live in a backup. A view has one
-- owner: the ID of the principal that made it, never the subject, and the
-- ID is not a foreign key because a view outlives the principal until the
-- cleanup of the owner runs. A view whose owner is not a principal matches
-- no reader. query_json holds the labels and references of the query in the
-- order that the person sent them. The name is unique for each owner in a
-- namespace, compared exactly.
CREATE TABLE saved_views (
    id            TEXT    NOT NULL PRIMARY KEY,
    namespace_id  TEXT    NOT NULL REFERENCES namespaces (id),
    owner         TEXT    NOT NULL,
    name          TEXT    NOT NULL,
    query_json    TEXT    NOT NULL,
    created_at_ms INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX saved_views_owner_name ON saved_views (namespace_id, owner, name);
CREATE INDEX saved_views_owner ON saved_views (owner);
