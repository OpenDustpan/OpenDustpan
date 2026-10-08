-- Namespaces: the unit of tenancy, authoritative in one home region.
CREATE TABLE namespaces (
    name             TEXT    NOT NULL PRIMARY KEY,
    home_region      TEXT    NOT NULL,
    failover_version INTEGER NOT NULL,
    created_at_ms    INTEGER NOT NULL
) STRICT;

-- Stacks: namespace/project/stack. Tags are a JSON object of strings.
CREATE TABLE stacks (
    namespace     TEXT    NOT NULL REFERENCES namespaces (name),
    project       TEXT    NOT NULL,
    name          TEXT    NOT NULL,
    tags_json     TEXT    NOT NULL,
    version       INTEGER NOT NULL,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    PRIMARY KEY (namespace, project, name)
) STRICT;
