-- Connections to a Git host. The tables hold three kinds of data.
--
-- git_host_app_registrations holds the GitHub App that the operator
-- registered for the whole installation. The private key and the event secret
-- are sealed under installation scopes, and no read answers either of them.
-- address_digest is a digest of the kind, the API address and the app ID that
-- the sealed key was stored for: the server refuses a key whose digest differs
-- from the address that the row holds now. hook_id names the registration in
-- the address of the webhook and does not change when a key is replaced.
--
-- git_host_installations is the map from a GitHub App installation to the
-- namespace that may use it. It is data of the installation: a namespace
-- backup does not hold it and only the operator changes it. The server reads
-- it at each use of a connection.
--
-- git_host_connections holds the connections of a namespace. A connection of
-- the installation-level app holds no credential and no address: both come
-- from the registration. A namespace has at most one default connection, and
-- at most one connection to each installation.
CREATE TABLE git_host_app_registrations (
    kind                       TEXT    NOT NULL PRIMARY KEY,
    app_id                     TEXT    NOT NULL,
    base_url                   TEXT    NOT NULL,
    hook_id                    TEXT    NOT NULL UNIQUE,
    sealed_key                 BLOB    NOT NULL,
    key_set_id                 TEXT    NOT NULL,
    key_created_at_ms          INTEGER NOT NULL,
    sealed_event_secret        BLOB    NOT NULL,
    event_secret_set_id        TEXT    NOT NULL,
    event_secret_created_at_ms INTEGER NOT NULL,
    address_digest             TEXT    NOT NULL,
    version                    INTEGER NOT NULL CHECK (version >= 1),
    created_at_ms              INTEGER NOT NULL,
    updated_at_ms              INTEGER NOT NULL
) STRICT;

CREATE TABLE git_host_installations (
    installation_id TEXT    NOT NULL PRIMARY KEY,
    namespace_id    TEXT    NOT NULL REFERENCES namespaces (id),
    version         INTEGER NOT NULL CHECK (version >= 1),
    created_at_ms   INTEGER NOT NULL,
    updated_at_ms   INTEGER NOT NULL
) STRICT;

CREATE INDEX git_host_installations_namespace ON git_host_installations (namespace_id);

CREATE TABLE git_host_connections (
    namespace_id    TEXT    NOT NULL REFERENCES namespaces (id),
    id              TEXT    NOT NULL,
    kind            TEXT    NOT NULL,
    auth            TEXT    NOT NULL,
    app_scope       TEXT    NOT NULL,
    installation_id TEXT    NOT NULL,
    push            INTEGER NOT NULL CHECK (push IN (0, 1)),
    is_default      INTEGER NOT NULL CHECK (is_default IN (0, 1)),
    version         INTEGER NOT NULL CHECK (version >= 1),
    created_at_ms   INTEGER NOT NULL,
    updated_at_ms   INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, id)
) STRICT;

CREATE UNIQUE INDEX git_host_connections_installation ON git_host_connections (namespace_id, kind, installation_id);
CREATE UNIQUE INDEX git_host_connections_default ON git_host_connections (namespace_id) WHERE is_default = 1;
