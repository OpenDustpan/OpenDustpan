-- Audit changes (ADR 63 decisions 9 and 10): the keys of an entry with
-- their old and new values, as the canonical JSON of audit.EncodeChanges.
-- The settings of a start entry, a reload of the configuration file and a
-- later change of a policy use it. The column stays NULL for rows written
-- before the field and for every other entry: a NULL means no changes.
ALTER TABLE audit_entries ADD COLUMN changes TEXT;
