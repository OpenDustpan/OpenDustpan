-- The configuration of the policy model: gates, access limits, approval
-- rules, ownership rules, path sets, and the approval default and the
-- signature carry-over setting of a scope. One row for each object. kind
-- names the kind of the object. id is the ID of the object, and empty for the
-- approval default and the carry-over setting, which a scope holds once.
-- The rows hold the stable ID of the namespace, never its name alone, and no
-- foreign key, so a namespace that a team creates again under the same name
-- has a new ID and reads none of them. namespace_id is empty for an object of
-- the installation. scope_key is the scope key of the scope that holds the
-- object. level, tool and unit_name let a query find the narrower scopes of a
-- scope. body holds the fields of the kind that no query reads.
CREATE TABLE gate_objects (
    namespace_id  TEXT    NOT NULL,
    scope_key     TEXT    NOT NULL,
    kind          TEXT    NOT NULL,
    id            TEXT    NOT NULL,
    level         TEXT    NOT NULL,
    tool          TEXT    NOT NULL,
    unit_name     TEXT    NOT NULL,
    version       INTEGER NOT NULL,
    body          TEXT    NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, scope_key, kind, id),
    CHECK (kind IN ('gate', 'access-limit', 'approval-rule', 'ownership-rule', 'path-set',
                    'approval-default', 'carry-over')),
    CHECK (level IN ('installation', 'namespace', 'collection', 'unit')),
    CHECK (version >= 1)
) STRICT;

-- The cross-scope checks find the objects of one kind and ID in other scopes.
CREATE INDEX gate_objects_kind_id ON gate_objects (kind, id);

-- The checks of a namespace find all its scopes.
CREATE INDEX gate_objects_namespace ON gate_objects (namespace_id, level);

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

-- The decision records. namespace_id is empty for a record of the
-- installation (an access limit on an installation object). A record has no
-- foreign key, for the same reason as the objects. refusal_key joins the
-- refusals of one principal and one limit; it is empty for any other record.
-- count and last_at_ms are the count of refusals that share the record and the
-- time of the last one; body holds the whole record.
CREATE TABLE gate_records (
    id            TEXT    NOT NULL PRIMARY KEY,
    namespace_id  TEXT    NOT NULL,
    point         TEXT    NOT NULL,
    change_kind   TEXT    NOT NULL,
    change_id     TEXT    NOT NULL,
    at_ms         INTEGER NOT NULL,
    last_at_ms    INTEGER NOT NULL,
    count         INTEGER NOT NULL,
    refusal_key   TEXT    NOT NULL,
    body          TEXT    NOT NULL
) STRICT;

-- ListByChange.
CREATE INDEX gate_records_change ON gate_records (namespace_id, change_kind, change_id, at_ms, id);

-- Query: the records of a decision point in a time range, newest first.
CREATE INDEX gate_records_point ON gate_records (namespace_id, point, at_ms, id);

-- NoteRefusal: the refusals of one key, newest first.
CREATE INDEX gate_records_refusal ON gate_records (namespace_id, refusal_key, last_at_ms) WHERE refusal_key <> '';
