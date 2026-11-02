-- The version table: one row for each migration that the store applied, with
-- the SHA-256 of its SQL. The migration runner creates the table before it
-- applies the schema file, and sqlc reads it with the schema file. It is
-- bookkeeping, not data, so a backup leaves it out.
CREATE TABLE IF NOT EXISTS schema_migrations (
    version            INTEGER NOT NULL PRIMARY KEY,
    name               TEXT    NOT NULL,
    min_binary_version TEXT    NOT NULL DEFAULT '',
    sha256             TEXT    NOT NULL DEFAULT ''
) STRICT;
