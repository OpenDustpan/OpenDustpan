-- State fence: the subject of the principal that forced the unlock whose
-- fence this version is (ADR 44, decision 1). The column stays empty for every
-- version that is not a fence, so rows from before this column read as not a
-- fence.
ALTER TABLE state_versions ADD COLUMN fenced_by TEXT NOT NULL DEFAULT '';
