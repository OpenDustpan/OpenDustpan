-- Custom roles and grants (ADR 52). A grant is a tuple: one subject, one
-- role and one object. subject is the text form of the subject:
-- principal:<id>, or group:<id>#member for each member of a group.
-- subject_kind and subject_id name it again, so the delete of a principal or
-- a group revokes its grants. role is the ID of a built-in role (its name)
-- or of a custom role (rol-...). object is the wire form of the object:
-- @server for the installation, a namespace, <namespace>/<collection> or a
-- unit reference, as it was at the time of the grant. target is what a
-- decision matches: unit:<stable ID> for a unit (ADR 19), so a grant follows
-- its unit and a new unit with the name of a deleted one does not get it,
-- and the wire form of the object for every other kind. object_kind and
-- namespace name the object again for lists.
-- condition stays NULL: it is room for the attribute conditions of ADR 52
-- decision 9, and no decision reads it yet. Built-in roles live in code, not
-- here.
--
-- Nothing is erased: a revoke sets revoked_at_ms and revoked_by, and the
-- delete of a custom role sets deleted_at_ms. The rows stay for the
-- access-review history (ADR 52, decision 10). The unique and lookup indexes
-- cover the rows that have not ended.
-- The key text columns of a grant use the C collation, so each comparison,
-- order and index compares bytes, as SQLite does.
CREATE TABLE roles (
    id            TEXT   NOT NULL PRIMARY KEY,
    name          TEXT   NOT NULL,
    name_key      TEXT   NOT NULL,
    description   TEXT   NOT NULL DEFAULT '',
    actions       TEXT   NOT NULL,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    deleted_at_ms BIGINT
);

CREATE UNIQUE INDEX roles_name_key ON roles (name_key COLLATE "C") WHERE deleted_at_ms IS NULL;

CREATE TABLE grants (
    id            TEXT   NOT NULL PRIMARY KEY,
    subject       TEXT   COLLATE "C" NOT NULL,
    subject_kind  TEXT   NOT NULL,
    subject_id    TEXT   NOT NULL,
    role          TEXT   COLLATE "C" NOT NULL,
    object        TEXT   COLLATE "C" NOT NULL,
    target        TEXT   COLLATE "C" NOT NULL,
    object_kind   TEXT   NOT NULL,
    namespace     TEXT   NOT NULL DEFAULT '',
    condition     TEXT,
    created_at_ms BIGINT NOT NULL,
    created_by    TEXT   NOT NULL DEFAULT '',
    revoked_at_ms BIGINT,
    revoked_by    TEXT   NOT NULL DEFAULT ''
);

-- One current grant of a role to a subject on an object. The index also
-- serves the decisions, which read the grants of a set of subjects.
CREATE UNIQUE INDEX grants_current ON grants (subject, role, target) WHERE revoked_at_ms IS NULL;

-- The grants on one object, in the order of a page.
CREATE INDEX grants_target ON grants (target, subject, role) WHERE revoked_at_ms IS NULL;

CREATE INDEX grants_role ON grants (role) WHERE revoked_at_ms IS NULL;

CREATE INDEX grants_holder ON grants (subject_kind, subject_id) WHERE revoked_at_ms IS NULL;
