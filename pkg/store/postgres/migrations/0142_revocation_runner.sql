-- The retire of a runner joins the writes that the journal of the stay in
-- maintenance mode holds: the kind runner. The restore retires the runner
-- again after the swap. The table lives in the control schema, so the DO block
-- names it there. The CHECK stays a defense-in-depth layer: the replay refuses
-- an unknown kind by itself.
DO $$
DECLARE
  ctl text := current_schema() || '_control';
BEGIN
  EXECUTE format('ALTER TABLE %I.maintenance_revocations DROP CONSTRAINT maintenance_revocations_kind', ctl);
  EXECUTE format(
    'ALTER TABLE %I.maintenance_revocations ADD CONSTRAINT maintenance_revocations_kind CHECK (kind IN (''session'', ''token'', ''principal'', ''temp-admin'', ''grant'', ''join-token'', ''provider'', ''runner''))',
    ctl);
END $$;
