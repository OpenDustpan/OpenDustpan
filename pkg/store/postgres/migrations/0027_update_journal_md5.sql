-- Journal blob MD5: the MD5 of the stored bytes of each batch, so the backup names
-- a journal blob the way it names a checkpoint blob (its MD5 and size). The column is
-- NOT NULL with no default. A database that holds journal rows fails this migration
-- with a NOT NULL error, because the MD5 of their bytes is not known in SQL. No
-- production row exists before item 9.
ALTER TABLE update_journal ADD COLUMN md5 BYTEA NOT NULL;

-- The journal closes when the build writes the checkpoint (ADR 43, decision 6.7). A
-- closed journal refuses every batch, so no batch can land after the build.
ALTER TABLE updates ADD COLUMN journal_closed BOOLEAN NOT NULL DEFAULT FALSE;
