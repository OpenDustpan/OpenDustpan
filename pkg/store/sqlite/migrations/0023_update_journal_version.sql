-- Update journal version: the journal version that the server answered when
-- the update started. Zero means the update saves checkpoints. Rows from
-- before this column read as zero, which is what they saved.
ALTER TABLE updates ADD COLUMN journal_version INTEGER NOT NULL DEFAULT 0;
