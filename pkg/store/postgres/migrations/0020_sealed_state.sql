-- Sealed OpenTofu state (ADR 41 decision 10). New state blobs and outputs
-- documents are sealed with the data key of the workspace scope. Rows without
-- the flag are read as before: issue #332 seals the existing rows.
ALTER TABLE state_versions ADD COLUMN sealed BOOLEAN NOT NULL DEFAULT FALSE;
