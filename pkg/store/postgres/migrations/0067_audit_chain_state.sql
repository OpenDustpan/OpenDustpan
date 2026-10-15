-- Audit chain state: the state of the audit hash chain that this
-- installation keeps, when it last changed and who changed it. No row means
-- the chain is on, as it was in every installation before it had a
-- setting. pending is set while a change of the chain waits for a person to
-- approve it, and keeps the installation in maintenance mode. log_replaced
-- is set by the swap of a restore, so a start after a crash still compares
-- the restored log with the state. The table is bookkeeping of the
-- installation, as schema_migrations is: a backup does not carry it, and a
-- restore keeps the row of the target.
CREATE TABLE audit_chain_state (
    id            SMALLINT PRIMARY KEY CHECK (id = 1),
    state         TEXT     NOT NULL CHECK (state IN ('on', 'off')),
    changed_at_ms BIGINT   NOT NULL,
    changed_by    TEXT     NOT NULL,
    pending       BOOLEAN  NOT NULL DEFAULT false,
    log_replaced  BOOLEAN  NOT NULL DEFAULT false
);
