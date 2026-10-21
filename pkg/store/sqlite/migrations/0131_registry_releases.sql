-- Registry releases belong to a namespace and live in a backup. A release is
-- one published version of a provider or a module. It never changes after the
-- insert, so the table has no update. The key is the address and the version:
-- system is the empty text for a provider. archives_json holds the archives of
-- the release in the order of the archive set. The bytes of the archives are
-- blobs under the prefix of the namespace, not rows. published_by is the ID of
-- the subject of the principal that published the release. protocols holds the
-- plugin protocol versions of a provider, joined by commas, and it is the empty
-- text for a module.
CREATE TABLE registry_releases (
    namespace_id     TEXT    NOT NULL REFERENCES namespaces (id),
    kind             TEXT    NOT NULL,
    name             TEXT    NOT NULL,
    system           TEXT    NOT NULL,
    version          TEXT    NOT NULL,
    sums_file        TEXT    NOT NULL,
    sums_sha256      TEXT    NOT NULL,
    signature_file   TEXT    NOT NULL,
    signature_sha256 TEXT    NOT NULL,
    key_fingerprint  TEXT    NOT NULL,
    protocols        TEXT    NOT NULL,
    published_by     TEXT    NOT NULL,
    archives_json    TEXT    NOT NULL,
    published_at_ms  INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, kind, name, system, version)
) STRICT;

-- Registry keys are the trusted signing keys of a namespace. A provider release
-- needs a signature by one of them. fingerprint is the fingerprint of the
-- primary key in uppercase hex, and armored_key is the public key in ASCII
-- armor. added_by is the ID of the principal that registered the key, and it is
-- not a foreign key because a key outlives the principal.
CREATE TABLE registry_keys (
    namespace_id TEXT NOT NULL REFERENCES namespaces (id),
    fingerprint  TEXT NOT NULL,
    armored_key  TEXT NOT NULL,
    added_by     TEXT NOT NULL,
    added_at_ms  INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, fingerprint)
) STRICT;
