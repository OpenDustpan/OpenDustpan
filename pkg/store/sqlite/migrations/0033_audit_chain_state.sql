-- Audit chain state (ADR 60 decisions 17 and 19): the state of the audit
-- hash chain that the installation recorded last. Empty means no record:
-- the server then keeps the chain on when the log holds chained entries,
-- and off otherwise. The server records 'on' or 'off' at each start, so a
-- later start sees a change of the setting and writes an audit entry for it.
ALTER TABLE installation ADD COLUMN audit_chain TEXT NOT NULL DEFAULT ''
    CHECK (audit_chain IN ('', 'on', 'off'));
