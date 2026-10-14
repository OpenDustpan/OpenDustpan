-- Audit chain state (ADR 60 decisions 17 and 19): the state of the audit
-- hash chain that this installation recorded last, and whether its log
-- ever held a chained entry. No row means no record: the server then reads
-- the log once and keeps the chain on when it holds chained entries. The
-- table is bookkeeping of the installation, as schema_migrations is: a
-- backup does not carry it, and a restore keeps the row of the target.
CREATE TABLE audit_chain_state (
    id           SMALLINT PRIMARY KEY CHECK (id = 1),
    state        TEXT     NOT NULL CHECK (state IN ('on', 'off')),
    ever_chained BOOLEAN  NOT NULL
);
