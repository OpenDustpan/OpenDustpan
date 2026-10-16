-- The minimum binary version of each applied migration. A migration sets it
-- with a header line "-- minimum-binary-version: vMAJOR.MINOR.PATCH", and
-- the runner records it here. The minimum of the schema is the highest
-- value. An empty value sets no minimum, so the rows before this migration
-- set none.
ALTER TABLE schema_migrations ADD COLUMN min_binary_version TEXT NOT NULL DEFAULT '';
