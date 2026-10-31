-- Workload trust rules. A rule belongs to one namespace and names a principal
-- of that namespace. namespace holds the name of the namespace, as
-- principals.namespace does: the table is access data, so a restore replaces
-- it with the principals table and never on its own. key_source is discovery
-- or static. static_keys holds the JWKS of a static rule and is empty for a
-- discovery rule. conditions_json holds the conditions on the claims, in the
-- order of their claim names. created_by is the subject of the principal that
-- made the rule. The name is unique in a namespace, compared exactly.
CREATE TABLE trust_rules (
    id              TEXT    NOT NULL PRIMARY KEY,
    namespace       TEXT    NOT NULL,
    name            TEXT    NOT NULL,
    issuer          TEXT    NOT NULL,
    audience        TEXT    NOT NULL,
    key_source      TEXT    NOT NULL CHECK (key_source IN ('discovery', 'static')),
    static_keys     TEXT    NOT NULL,
    conditions_json TEXT    NOT NULL,
    principal_id    TEXT    NOT NULL REFERENCES principals (id),
    created_by      TEXT    NOT NULL,
    created_at_ms   INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX trust_rules_name ON trust_rules (namespace, name);
CREATE INDEX trust_rules_principal ON trust_rules (principal_id);
