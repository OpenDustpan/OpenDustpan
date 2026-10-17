-- No schema change: SQLite keeps the control tables in the same file and the
-- restore copies them into the staged file, so no new schema object is
-- needed. The version keeps both dialects on the same schema version.
SELECT 1;
