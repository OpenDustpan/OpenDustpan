-- The version row of each running server process: the instance ID, the binary
-- version and the start time. A process writes its row at start-up and deletes
-- it at a clean stop. The row is not a liveness lease: nothing renews it, and
-- nothing reads it to decide that an instance is live. The installation API
-- lists the rows, and an administrator deletes the row of a process that died.
-- A contract migration that raises the minimum binary version does not run
-- while a row below the new minimum exists.
-- The table is part of the control schema, which the swap of a restore never
-- renames, so the rows stay across a restore. A backup leaves the table out.
--
-- The CHECK constraints are a defense-in-depth layer. The store validates the
-- ID and the version before it writes, without relying on them.
CREATE TABLE instance_versions (
    id             text   PRIMARY KEY,
    binary_version text   NOT NULL,
    started_at_ms  bigint NOT NULL,
    CONSTRAINT instance_versions_id CHECK (length(id) BETWEEN 1 AND 64),
    CONSTRAINT instance_versions_binary_version CHECK (length(binary_version) BETWEEN 1 AND 64)
);

DO $$
DECLARE
  live text := current_schema();
  ctl text := current_schema() || '_control';
BEGIN
  EXECUTE format('CREATE SCHEMA IF NOT EXISTS %I', ctl);
  EXECUTE format('ALTER TABLE %I.instance_versions SET SCHEMA %I', live, ctl);
END $$;
