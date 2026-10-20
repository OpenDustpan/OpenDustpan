-- The report of the dev data that a restore removed, or would remove for a
-- check-only restore, as JSON, with the codes of the warnings that the answer
-- of the restore carries. The restores table survives the swap of a restore,
-- so the report stays when an audit entry cannot be written. An empty text
-- means that no report was stored.
ALTER TABLE restores ADD COLUMN dev_report TEXT NOT NULL DEFAULT '';
