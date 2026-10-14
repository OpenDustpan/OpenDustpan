-- Soft delete of workspaces (ADR 47), as 0031 does for stacks: a delete keeps
-- the workspace with its state versions and data key until the retention
-- period ends. The delete moves the name to deleted_name and sets the name to
-- "deleted:" and the workspace ID. A workspace name cannot hold ":", so the
-- deleted row never matches a lookup by name, and the name is free at once.
-- deleted_at_ms is the time of the delete, and NULL for a live workspace.
ALTER TABLE workspaces ADD COLUMN deleted_at_ms BIGINT;
ALTER TABLE workspaces ADD COLUMN deleted_name TEXT;
