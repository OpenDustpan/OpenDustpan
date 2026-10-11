-- Update history fields (ADR 28 decision 4): the subject of the principal
-- that created the update, and the stack version a restore copied. The
-- defaults fill every row from before this migration: no creator and no
-- restore source. The checkpoint size travels on reads, not in this table.
ALTER TABLE updates ADD COLUMN created_by TEXT NOT NULL DEFAULT '';
ALTER TABLE updates ADD COLUMN restored_from INTEGER NOT NULL DEFAULT 0;
