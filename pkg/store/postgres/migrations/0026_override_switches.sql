-- Override switches (ADR 44, decision 6): a namespace and a unit each allow a
-- forced write and a write without the lock. Both stay off until a switch is
-- set. A unit switch only narrows its namespace switch. The version flag
-- marks a version declared without the lock (decision 5), so its finalize
-- does not check the lock.
ALTER TABLE namespaces ADD COLUMN allow_forced_writes BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE namespaces ADD COLUMN allow_unlocked_writes BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE workspaces ADD COLUMN allow_forced_writes BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE workspaces ADD COLUMN allow_unlocked_writes BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE state_versions ADD COLUMN unlocked_write BOOLEAN NOT NULL DEFAULT FALSE;
