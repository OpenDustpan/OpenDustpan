-- The configuration of a unit keys on the stable ID of the unit, not on its
-- name. A rename and a move between collections keep the rows with no write,
-- and a new unit that takes the name of a deleted unit has no rows. No tag
-- shipped the tables of 0110, so the tables are made again with no copy of
-- the rows.
--
-- scope_key is the scope key of the scope that holds the object, for each
-- level but a unit. For a unit, it is unit/ and the stable ID of the unit, and
-- unit_id holds that ID. unit_id is empty for each other level. The server
-- finds the unit of a name in the inventory, in the transaction of the read or
-- the write. The rows of a deleted unit stay until the purge of the unit,
-- which deletes them: a restore keeps the ID, so it gets them back. level lets
-- a query find the narrower scopes of a scope.
DROP TABLE gate_references;
DROP TABLE gate_objects;

CREATE TABLE gate_objects (
    namespace_id  TEXT NOT NULL,
    scope_key     TEXT NOT NULL,
    kind          TEXT NOT NULL,
    id            TEXT NOT NULL,
    level         TEXT NOT NULL,
    unit_id       TEXT NOT NULL,
    version       INTEGER NOT NULL,
    body          TEXT NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, scope_key, kind, id),
    CHECK (kind IN ('gate', 'access-limit', 'approval-rule', 'ownership-rule', 'path-set',
                    'approval-default', 'carry-over')),
    CHECK (level IN ('installation', 'namespace', 'collection', 'unit')),
    CHECK ((level = 'unit') = (unit_id <> '')),
    CHECK (level <> 'unit' OR scope_key = 'unit/' || unit_id),
    CHECK (version >= 1)
) STRICT;

-- The cross-scope checks find the objects of one kind and ID in other scopes.
CREATE INDEX gate_objects_kind_id ON gate_objects (kind, id);

-- The checks of a namespace find all its scopes.
CREATE INDEX gate_objects_namespace ON gate_objects (namespace_id, level);

-- The purge of a unit finds its rows.
CREATE INDEX gate_objects_unit ON gate_objects (namespace_id, unit_id);

-- The references of an object to the approval rules and the path sets that it
-- names. The removal of a rule or of a path set finds its readers here. A row
-- goes with its owner.
CREATE TABLE gate_references (
    ref_kind     TEXT NOT NULL,
    ref_id       TEXT NOT NULL,
    namespace_id TEXT NOT NULL,
    scope_key    TEXT NOT NULL,
    owner_kind   TEXT NOT NULL,
    owner_id     TEXT NOT NULL,
    PRIMARY KEY (ref_kind, ref_id, namespace_id, scope_key, owner_kind, owner_id),
    FOREIGN KEY (namespace_id, scope_key, owner_kind, owner_id)
        REFERENCES gate_objects (namespace_id, scope_key, kind, id) ON DELETE CASCADE,
    CHECK (ref_kind IN ('approval-rule', 'path-set'))
) STRICT;

-- The cascade finds the references of an owner.
CREATE INDEX gate_references_owner ON gate_references (namespace_id, scope_key, owner_kind, owner_id);
