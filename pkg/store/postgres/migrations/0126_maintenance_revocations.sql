-- The journal of the writes that only reduce access (a sign-out, a session or
-- token revocation, the disable of a principal) and that ran while the
-- installation was in maintenance mode. A restore replaces the access tables
-- with the backup, so it would lose these writes. The restore applies the
-- journal again after the swap. The table is part of the control schema, which
-- the swap never renames, so the rows stay across the swap. A backup leaves
-- the table out. Entering maintenance mode clears it and starts a new stay in maintenance mode.
CREATE TABLE maintenance_revocations (
    id         bigserial PRIMARY KEY,
    kind       text   NOT NULL,
    target     text   NOT NULL,
    end_reason text   NOT NULL,
    at_ms      bigint NOT NULL,
    CONSTRAINT maintenance_revocations_kind CHECK (kind IN ('session', 'token', 'principal'))
);

DO $$
DECLARE
  live text := current_schema();
  ctl text := current_schema() || '_control';
BEGIN
  EXECUTE format('CREATE SCHEMA IF NOT EXISTS %I', ctl);
  EXECUTE format('ALTER TABLE %I.maintenance_revocations SET SCHEMA %I', live, ctl);
END $$;
