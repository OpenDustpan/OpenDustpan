-- Principals and groups of the installation (ADR 52, decisions 1, 2 and 4).
-- A principal is a person, a service or a workload. Its ID is opaque, with
-- the prefix prn-. Subject is the name that an Authenticator answers, unique
-- among the principals that exist and compared exactly. A group is a named
-- set of principals. Its ID is opaque, with the prefix grp-. name_key holds
-- the name in lower case, so two groups that exist never have the same name
-- without regard to case. A membership puts one principal in one group from
-- added_at_ms until removed_at_ms.
--
-- Nothing is erased: a delete sets deleted_at_ms, and a removal or the
-- delete of a principal or a group sets removed_at_ms. The rows stay for the
-- access-review history (ADR 52, decision 10), so no foreign key cascades.
-- The unique and lookup indexes cover the rows that have not ended. The rows
-- carry no fence: they describe the installation, not one namespace.
-- The text indexes use the C collation, so the ordered and prefix reads
-- compare bytes and use the index.
CREATE TABLE principals (
    id            TEXT   NOT NULL PRIMARY KEY,
    subject       TEXT   NOT NULL,
    display_name  TEXT   NOT NULL DEFAULT '',
    kind          TEXT   NOT NULL,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    deleted_at_ms BIGINT
);

CREATE UNIQUE INDEX principals_subject ON principals (subject COLLATE "C") WHERE deleted_at_ms IS NULL;

CREATE TABLE principal_groups (
    id            TEXT   NOT NULL PRIMARY KEY,
    name          TEXT   NOT NULL,
    name_key      TEXT   NOT NULL,
    description   TEXT   NOT NULL DEFAULT '',
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    deleted_at_ms BIGINT
);

CREATE UNIQUE INDEX principal_groups_name_key ON principal_groups (name_key COLLATE "C") WHERE deleted_at_ms IS NULL;

CREATE TABLE group_members (
    group_id      TEXT   NOT NULL REFERENCES principal_groups (id),
    principal_id  TEXT   NOT NULL REFERENCES principals (id),
    added_at_ms   BIGINT NOT NULL,
    removed_at_ms BIGINT,
    PRIMARY KEY (group_id, principal_id, added_at_ms)
);

-- One current membership of a principal in a group. The index also serves
-- the members of a group.
CREATE UNIQUE INDEX group_members_current ON group_members (group_id, principal_id) WHERE removed_at_ms IS NULL;

CREATE INDEX group_members_principal ON group_members (principal_id) WHERE removed_at_ms IS NULL;
