-- The state version this version was restored from (ADR 28 decision 3).
-- Empty unless the version is a restore. A restore is a forced version that
-- repeats the lineage, the serial, the MD5 and the size of the old one, so
-- the existing create and finalize calls store it: no new write exists.
ALTER TABLE state_versions ADD COLUMN restored_from TEXT NOT NULL DEFAULT '';

CREATE INDEX state_versions_workspace_finalized ON state_versions (workspace_id, finalized_at_ms DESC);
