-- History fields of updates (ADR 28 decision 4): who created the update and
-- which stack version a restore copied. Old rows keep an empty creator and 0.
ALTER TABLE updates ADD COLUMN created_by TEXT NOT NULL DEFAULT '';

ALTER TABLE updates ADD COLUMN restored_from BIGINT NOT NULL DEFAULT 0;
