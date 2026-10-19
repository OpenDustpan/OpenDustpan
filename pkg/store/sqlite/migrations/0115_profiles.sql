-- Profiles: named sets of config, secrets and credentials in a namespace.
-- All rows hold the stable ID of the namespace, never its name. A save adds a
-- revision that never changes. The document of a revision holds each secret
-- sealed. deleted_at_ms is 0 for a live profile, and a name is unique among
-- the live profiles of a namespace.
CREATE TABLE profiles (
    namespace_id  TEXT    NOT NULL,
    id            TEXT    NOT NULL,
    name          TEXT    NOT NULL,
    latest        INTEGER NOT NULL,
    created_by    TEXT    NOT NULL,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    deleted_at_ms INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, id),
    CHECK (latest >= 1)
) STRICT;

CREATE UNIQUE INDEX profiles_live_name ON profiles (namespace_id, name) WHERE deleted_at_ms = 0;

CREATE TABLE profile_revisions (
    namespace_id  TEXT    NOT NULL,
    profile_id    TEXT    NOT NULL,
    number        INTEGER NOT NULL,
    document      TEXT    NOT NULL,
    document_sha256 TEXT  NOT NULL,
    author        TEXT    NOT NULL,
    created_at_ms INTEGER NOT NULL,
    reason        TEXT    NOT NULL,
    PRIMARY KEY (namespace_id, profile_id, number),
    FOREIGN KEY (namespace_id, profile_id) REFERENCES profiles (namespace_id, id),
    CHECK (number >= 1)
) STRICT;
