-- Catalog blueprints belong to a namespace and live in a backup. A blueprint
-- has one row with its one draft: the draft document in draft_json and the
-- draft version, which starts at 1 and grows by 1 at each write. The row
-- stays when the blueprint has no published version, and the number of rows
-- in a namespace is the number of its blueprints. The name is unique in the
-- namespace, compared exactly.
CREATE TABLE catalog_blueprints (
    namespace_id     TEXT    NOT NULL REFERENCES namespaces (id),
    name             TEXT    NOT NULL,
    draft_version    INTEGER NOT NULL CHECK (draft_version >= 1),
    draft_json       TEXT    NOT NULL,
    draft_updated_at_ms INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, name)
) STRICT;

-- The published versions of a blueprint. The number is the integer after the
-- last one, from 1. document_json, published_by and published_at_ms never
-- change after the insert. Only retired and expr_version change: a retire
-- sets retired to 1, and a compile with a new Expr version sets expr_version.
-- expr_version holds the Expr version of the blueprint version, and
-- document_json holds the document with no Expr version. The flag retired is
-- 0 or 1. A version outlives no blueprint: the foreign key names the blueprint
-- row, which a user never deletes.
CREATE TABLE catalog_blueprint_versions (
    namespace_id    TEXT    NOT NULL,
    name            TEXT    NOT NULL,
    number          INTEGER NOT NULL CHECK (number >= 1),
    document_json   TEXT    NOT NULL,
    expr_version    TEXT    NOT NULL,
    published_by    TEXT    NOT NULL,
    published_at_ms INTEGER NOT NULL,
    retired         INTEGER NOT NULL CHECK (retired IN (0, 1)),
    PRIMARY KEY (namespace_id, name, number),
    FOREIGN KEY (namespace_id, name) REFERENCES catalog_blueprints (namespace_id, name)
) STRICT;
