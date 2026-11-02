-- The database layer of the settings of the installation and of the
-- namespaces. One row holds the value that the API set for one key. A key
-- with no row has no database value. scope_id is '@server' for the
-- installation and the stable ID of the namespace for a namespace, so a
-- later namespace of the same name has no row of the purged one. A purge of a
-- namespace must delete the rows of its ID. The text of a value is the text
-- form of the kind of the setting. The server never writes the value of a
-- secret setting here.
CREATE TABLE settings (
    scope_id      text   NOT NULL,
    key           text   NOT NULL CHECK (char_length(key) BETWEEN 1 AND 64),
    value         text   NOT NULL CHECK (char_length(value) <= 1024),
    updated_at_ms bigint NOT NULL,
    PRIMARY KEY (scope_id, key)
);

-- One revision for each scope. The revision changes at each change of a
-- database value, and when the static layer changes an effective value or a
-- source. static_digest is the digest of the static layer at the last sync,
-- empty for a namespace. It holds no part of a secret.
CREATE TABLE settings_state (
    scope_id      text   NOT NULL PRIMARY KEY,
    revision      bigint NOT NULL CHECK (revision >= 0),
    static_digest text   NOT NULL
);
