-- Inventory locks: which unit is locked, by whom, and since when. Each tool
-- package publishes its lock changes to these columns in the same
-- transaction as the lock itself. The columns stay NULL when the unit is not
-- locked. lock_holder is the subject of the principal that holds the lock,
-- never a token or a secret. lock_since_ms is the take time in Unix
-- milliseconds, as last_changed_ms is. lock_operation is the tool's own word
-- for what holds the lock.
ALTER TABLE inventory ADD COLUMN lock_holder TEXT;
ALTER TABLE inventory ADD COLUMN lock_since_ms BIGINT;
ALTER TABLE inventory ADD COLUMN lock_operation TEXT;
