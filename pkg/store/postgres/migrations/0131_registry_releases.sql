-- Registry releases belong to a namespace and live in a backup. A release is
-- one published version of a provider or a module. It never changes after the
-- insert, so the table has no update. The key is the address and the version:
-- system is the empty text for a provider. archives_json holds the archives of
-- the release in the order of the archive set. The bytes of the archives are
-- blobs under the prefix of the namespace, not rows. published_by is the ID of
-- the principal that published the release, and it is not a foreign key
-- because a release outlives the principal.
CREATE TABLE registry_releases (
    namespace_id     text    NOT NULL REFERENCES namespaces (id),
    kind             text    NOT NULL,
    name             text    NOT NULL,
    system           text    NOT NULL,
    version          text    NOT NULL,
    sums_file        text    NOT NULL,
    sums_sha256      text    NOT NULL,
    signature_file   text    NOT NULL,
    signature_sha256 text    NOT NULL,
    key_fingerprint  text    NOT NULL,
    published_by     text    NOT NULL,
    archives_json    text    NOT NULL,
    published_at_ms  bigint  NOT NULL,
    PRIMARY KEY (namespace_id, kind, name, system, version)
);
