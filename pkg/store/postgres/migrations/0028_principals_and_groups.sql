-- Principals and groups of the installation (ADR 52, decisions 1, 2 and 4).
-- A principal is a person, a service or a workload. Its ID is opaque, with
-- the prefix prn-. Subject is the name that an Authenticator answers, unique
-- in the installation and compared exactly. A group is a named set of
-- principals. Its ID is opaque, with the prefix grp-. name_key holds the
-- name in lower case, so two groups never have the same name without regard
-- to case. A membership puts one principal in one group. A delete of a
-- principal or a group deletes its memberships. The rows carry no fence:
-- they describe the installation, not one namespace.
CREATE TABLE principals (
    id            TEXT    NOT NULL PRIMARY KEY,
    subject       TEXT    NOT NULL UNIQUE,
    display_name  TEXT    NOT NULL DEFAULT '',
    kind          TEXT    NOT NULL,
    disabled      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at_ms BIGINT  NOT NULL,
    updated_at_ms BIGINT  NOT NULL
);

CREATE TABLE principal_groups (
    id            TEXT   NOT NULL PRIMARY KEY,
    name          TEXT   NOT NULL,
    name_key      TEXT   NOT NULL UNIQUE,
    description   TEXT   NOT NULL DEFAULT '',
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL
);

CREATE TABLE group_members (
    group_id      TEXT   NOT NULL REFERENCES principal_groups (id) ON DELETE CASCADE,
    principal_id  TEXT   NOT NULL REFERENCES principals (id) ON DELETE CASCADE,
    added_at_ms   BIGINT NOT NULL,
    PRIMARY KEY (group_id, principal_id)
);

CREATE INDEX group_members_principal ON group_members (principal_id);
