-- The installation kind: which command made the data of this installation.
-- The value is dev, production or NULL. The baseline row has NULL, and the
-- first command that starts on the database claims it with one conditional
-- UPDATE. No default sets a kind, so a crash between this migration and the
-- claim leaves NULL and never a wrong kind. A new kind needs a migration that
-- rebuilds the CHECK constraint.
-- The installation table lives in the control schema. The search path of the
-- store holds it, and so does the search path of the staging schema of a
-- restore, so the statements need no schema name.
ALTER TABLE installation ADD COLUMN kind text;
ALTER TABLE installation
    ADD CONSTRAINT installation_kind_check CHECK (kind IS NULL OR kind IN ('dev', 'production'));
