-- The OIDC issuer of a namespace: its signing keys and its settings. All rows
-- hold the stable ID of the namespace, never its name. A key has the state
-- next, active, retired or deleted. A time that does not apply is 0. The
-- sealed column holds the private key, sealed under the data key of the
-- namespace scope issuer, for a key of the state next or active only: a key
-- that retires or is deleted drops it. The public_key column holds the PKIX
-- DER of the public key.
CREATE TABLE issuer_keys (
    namespace_id    TEXT    NOT NULL,
    id              TEXT    NOT NULL,
    state           TEXT    NOT NULL,
    public_key      BLOB    NOT NULL,
    sealed          BLOB    NOT NULL,
    created_at_ms   INTEGER NOT NULL,
    activated_at_ms INTEGER NOT NULL,
    retired_at_ms   INTEGER NOT NULL,
    deleted_at_ms   INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, id),
    CHECK (state IN ('next', 'active', 'retired', 'deleted')),
    CHECK ((state IN ('next', 'active')) = (length(sealed) > 0))
) STRICT;

-- A namespace has at most one active key and one next key. This is a layer
-- of its own: the store also checks the rule before it writes.
CREATE UNIQUE INDEX issuer_keys_one_active ON issuer_keys (namespace_id) WHERE state = 'active';
CREATE UNIQUE INDEX issuer_keys_one_next ON issuer_keys (namespace_id) WHERE state = 'next';

-- The settings of the issuer of a namespace. A namespace with no row has the
-- defaults at version 0. subject_template is a JSON list of keys. copy_check
-- is the JSON of the last copy check, and the empty text before the first.
CREATE TABLE issuer_settings (
    namespace_id         TEXT    NOT NULL,
    issuer_url           TEXT    NOT NULL,
    subject_template     TEXT    NOT NULL,
    allow_block_subjects INTEGER NOT NULL,
    include_actor_name   INTEGER NOT NULL,
    version              INTEGER NOT NULL,
    updated_at_ms        INTEGER NOT NULL,
    updated_by           TEXT    NOT NULL,
    copy_check           TEXT    NOT NULL,
    PRIMARY KEY (namespace_id),
    CHECK (allow_block_subjects IN (0, 1)),
    CHECK (include_actor_name IN (0, 1)),
    CHECK (version >= 0)
) STRICT;
