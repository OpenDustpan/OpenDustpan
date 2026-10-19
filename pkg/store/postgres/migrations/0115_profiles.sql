-- Profiles: named sets of config, secrets and credentials in a namespace.
-- All rows hold the stable ID of the namespace, never its name. A save adds a
-- revision that never changes. The document of a revision holds each secret
-- sealed. deleted_at_ms is 0 for a live profile, and a name is unique among
-- the live profiles of a namespace.
CREATE TABLE profiles (
    namespace_id text NOT NULL,
    id text NOT NULL,
    name text NOT NULL,
    latest        bigint NOT NULL,
    created_by text NOT NULL,
    created_at_ms bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    deleted_at_ms bigint NOT NULL,
    PRIMARY KEY (namespace_id, id),
    CHECK (latest >= 1)
);

CREATE UNIQUE INDEX profiles_live_name ON profiles (namespace_id, name) WHERE deleted_at_ms = 0;

CREATE TABLE profile_revisions (
    namespace_id text NOT NULL,
    profile_id text NOT NULL,
    number        bigint NOT NULL,
    document text NOT NULL,
    document_sha256 text NOT NULL,
    author text NOT NULL,
    created_at_ms bigint NOT NULL,
    reason text NOT NULL,
    PRIMARY KEY (namespace_id, profile_id, number),
    FOREIGN KEY (namespace_id, profile_id) REFERENCES profiles (namespace_id, id),
    CHECK (number >= 1)
);
