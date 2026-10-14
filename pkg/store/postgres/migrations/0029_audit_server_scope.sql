-- Audit entries of the installation (ADR 60, decisions 6 and 7). An action
-- that is not in a namespace, for example a change of a principal or a
-- group, writes to the scope @server, with its own sequence and chain. The
-- namespace column holds the scope, so it can no longer name a row of
-- namespaces: this migration drops that foreign key. The rows, the columns
-- and the index stay as they were.
ALTER TABLE audit_entries DROP CONSTRAINT audit_entries_namespace_fkey;
