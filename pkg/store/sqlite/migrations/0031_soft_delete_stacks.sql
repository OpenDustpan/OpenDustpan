-- Soft delete of stacks (ADR 47): a delete keeps the stack with its updates,
-- checkpoints, events, tags and data key until the retention period ends.
-- The delete moves the name of the stack to deleted_name and sets the name to
-- "deleted:" and the stable ID. A stack name cannot hold ":", so the deleted
-- row never matches a lookup by name, and the name is free at once for a new
-- stack. deleted_at_ms is the time of the delete, and NULL for a live stack.
ALTER TABLE stacks ADD COLUMN deleted_at_ms INTEGER;
ALTER TABLE stacks ADD COLUMN deleted_name TEXT;
