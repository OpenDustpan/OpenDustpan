-- Audit chain state (ADR 60 decisions 17 and 19): the state of the audit
-- hash chain that this installation recorded last, and whether its log
-- ever held a chained entry. No row means no record: the server then reads
-- the log once and keeps the chain on when it holds chained entries. The
-- table is bookkeeping of the installation, as schema_migrations is: a
-- backup does not carry it, and a restore keeps the row of the target.
-- The restore columns name the restore whose swap the server waits for.
-- The swap copies them into the restored data and sets restore_applied, so
-- a start after a crash knows that the log was replaced and that the
-- switch entry of the restore is still missing.
CREATE TABLE audit_chain_state (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    state        TEXT    NOT NULL CHECK (state IN ('on', 'off')),
    ever_chained INTEGER NOT NULL CHECK (ever_chained IN (0, 1)),
    restore_id           TEXT    NOT NULL DEFAULT '',
    restore_backup_id    TEXT    NOT NULL DEFAULT '',
    restore_requested_by TEXT    NOT NULL DEFAULT '',
    restore_applied      INTEGER NOT NULL DEFAULT 0 CHECK (restore_applied IN (0, 1))
) STRICT;
