-- The minimum binary version of each applied migration. A migration sets it
-- with a header line "-- minimum-binary-version: vMAJOR.MINOR.PATCH", and
-- the runner records it here. The minimum of the schema is the highest
-- value. An empty value sets no minimum, so the rows before this migration
-- set none.
--
-- The runner creates schema_migrations before any migration runs, so the
-- CREATE below changes nothing. It declares the table for sqlc, which reads
-- the schema from the migration files only.
CREATE TABLE IF NOT EXISTS schema_migrations (
    version BIGINT NOT NULL PRIMARY KEY,
    name    TEXT   NOT NULL
);

ALTER TABLE schema_migrations ADD COLUMN min_binary_version TEXT NOT NULL DEFAULT '';
