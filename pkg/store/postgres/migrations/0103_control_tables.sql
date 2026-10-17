-- Move the installation, backups and restores tables into a control schema
-- that the restore swap never renames. The live rows stay across the swap,
-- so the running restore stays readable during the swap and the pre-backup
-- row survives it. The control schema is per live schema: it appends
-- _control to the current schema, so each test schema keeps its own control
-- tables. The swap renames only the live and staging schemas.
DO $$
DECLARE
  live text := current_schema();
  ctl text := current_schema() || '_control';
BEGIN
  EXECUTE format('CREATE SCHEMA IF NOT EXISTS %I', ctl);
  EXECUTE format('ALTER TABLE %I.installation SET SCHEMA %I', live, ctl);
  EXECUTE format('ALTER TABLE %I.backups SET SCHEMA %I', live, ctl);
  EXECUTE format('ALTER TABLE %I.restores SET SCHEMA %I', live, ctl);
END $$;
