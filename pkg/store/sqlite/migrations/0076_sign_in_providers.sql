-- Sign-in providers that an administrator manages through the API, and the
-- data keys of values that belong to the installation.
--
-- installation_secret_keys holds one wrapped data key for each installation
-- scope, as secret_keys does for each namespace scope. A value in an
-- installation scope belongs to no namespace, so the rows carry no fence.
--
-- sign_in_providers holds the OpenID Connect providers of the console
-- sign-in that the API created. A provider from the flags, the environment
-- or the configuration file is never written here. client_secret is sealed
-- in the installation scope of the provider, and the API never answers it.
-- scopes is a JSON list. version counts the changes of a row, so a change
-- refuses to overwrite a change that it did not read. A provider is
-- disabled, not deleted, while a principal has a subject of its ID.
CREATE TABLE installation_secret_keys (
    scope_id      TEXT    NOT NULL PRIMARY KEY,
    wrapper_id    TEXT    NOT NULL,
    ciphertext    BLOB    NOT NULL,
    created_at_ms INTEGER NOT NULL
) STRICT;

CREATE TABLE sign_in_providers (
    provider_id   TEXT    NOT NULL PRIMARY KEY,
    issuer        TEXT    NOT NULL,
    display_name  TEXT    NOT NULL,
    client_id     TEXT    NOT NULL,
    client_secret BLOB    NOT NULL,
    scopes        TEXT    NOT NULL,
    auto_create   INTEGER NOT NULL,
    enabled       INTEGER NOT NULL,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    version       INTEGER NOT NULL
) STRICT;
