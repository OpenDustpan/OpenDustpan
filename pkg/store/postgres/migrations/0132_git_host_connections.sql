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
    kind                       text    NOT NULL PRIMARY KEY,
    app_id                     text    NOT NULL,
    base_url                   text    NOT NULL,
    hook_id                    text    NOT NULL UNIQUE,
    sealed_key                 bytea   NOT NULL,
    key_set_id                 text    NOT NULL,
    key_created_at_ms          bigint  NOT NULL,
    sealed_event_secret        bytea   NOT NULL,
    event_secret_set_id        text    NOT NULL,
    event_secret_created_at_ms bigint  NOT NULL,
    address_digest             text    NOT NULL,
    version                    bigint  NOT NULL CHECK (version >= 1),
    created_at_ms              bigint  NOT NULL,
    updated_at_ms              bigint  NOT NULL
);

CREATE TABLE git_host_installations (
    installation_id text    NOT NULL PRIMARY KEY,
    namespace_id    text    NOT NULL REFERENCES namespaces (id),
    version         bigint  NOT NULL CHECK (version >= 1),
    created_at_ms   bigint  NOT NULL,
    updated_at_ms   bigint  NOT NULL
);

CREATE INDEX git_host_installations_namespace ON git_host_installations (namespace_id);

CREATE TABLE git_host_connections (
    namespace_id    text    NOT NULL REFERENCES namespaces (id),
    id              text    NOT NULL,
    kind            text    NOT NULL,
    auth            text    NOT NULL,
    app_scope       text    NOT NULL,
    installation_id text    NOT NULL,
    push            boolean  NOT NULL,
    is_default      boolean  NOT NULL,
    version         bigint  NOT NULL CHECK (version >= 1),
    created_at_ms   bigint  NOT NULL,
    updated_at_ms   bigint  NOT NULL,
    PRIMARY KEY (namespace_id, id)
);

CREATE UNIQUE INDEX git_host_connections_installation ON git_host_connections (namespace_id, kind, installation_id);
CREATE UNIQUE INDEX git_host_connections_default ON git_host_connections (namespace_id) WHERE is_default;
