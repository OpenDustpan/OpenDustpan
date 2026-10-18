-- Context of objects: the summary, runbook, owners, on-call and links that a
-- person set through the API or that the code of a unit sets as an output.
-- One row for each entry. The rows key by the stable ID of the namespace, so
-- a namespace created again under an old name reads none of them. object_id is
-- empty for a namespace, else the ID of the collection, the unit or the
-- blueprint; a resource has the ID of its unit and its resource_key.
CREATE TABLE object_context (
    namespace_id           TEXT    NOT NULL REFERENCES namespaces (id),
    object_kind            TEXT    NOT NULL,
    object_id              TEXT    NOT NULL,
    tool                   TEXT    NOT NULL,
    resource_key           TEXT    NOT NULL,
    source                 TEXT    NOT NULL,
    fields                 TEXT    NOT NULL,
    warning                TEXT    NOT NULL,
    item_id                TEXT    NOT NULL,
    item_seq               INTEGER NOT NULL,
    resource_deleted_at_ms INTEGER,
    marks_seq              INTEGER NOT NULL,
    updated_at_ms          INTEGER NOT NULL,
    updated_by             TEXT    NOT NULL,
    PRIMARY KEY (namespace_id, object_kind, object_id, resource_key, source),
    CHECK (object_kind IN ('namespace', 'collection', 'unit', 'resource', 'blueprint')),
    CHECK (source IN ('api', 'output'))
) STRICT;

-- The owner index: one row for each owner of each entry. The primary key puts
-- the rows of one owner in one namespace in one range. A row goes with its
-- entry.
CREATE TABLE object_context_owners (
    namespace_id TEXT NOT NULL,
    owner        TEXT NOT NULL,
    object_kind  TEXT NOT NULL,
    object_id    TEXT NOT NULL,
    resource_key TEXT NOT NULL,
    source       TEXT NOT NULL,
    PRIMARY KEY (namespace_id, owner, object_kind, object_id, resource_key, source),
    FOREIGN KEY (namespace_id, object_kind, object_id, resource_key, source)
        REFERENCES object_context (namespace_id, object_kind, object_id, resource_key, source)
        ON DELETE CASCADE
) STRICT;

-- The cascade finds the owner rows of an entry by the entry key.
CREATE INDEX object_context_owners_entry
    ON object_context_owners (namespace_id, object_kind, object_id, resource_key, source);
