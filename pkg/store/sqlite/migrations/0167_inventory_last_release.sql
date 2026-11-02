-- The last released lock of each unit in the inventory projection: the lock
-- ID, the holder, the principal that released the lock, the time of the
-- release and the kind of release. The workspace package writes these columns
-- in the same transaction as the record on the workspace, so that the unit
-- view and the inventory view can show why a repeated unlock succeeded. A
-- unit without a record keeps empty text, the time 0 and the kind ''. A
-- Pulumi stack never has a record. The CHECK refuses a row with another kind.
ALTER TABLE inventory ADD COLUMN last_release_lock_id TEXT NOT NULL DEFAULT '';
ALTER TABLE inventory ADD COLUMN last_release_holder TEXT NOT NULL DEFAULT '';
ALTER TABLE inventory ADD COLUMN last_release_by TEXT NOT NULL DEFAULT '';
ALTER TABLE inventory ADD COLUMN last_release_kind TEXT NOT NULL DEFAULT ''
    CHECK (last_release_kind IN ('', 'normal', 'forced'));
ALTER TABLE inventory ADD COLUMN last_release_at_ms INTEGER NOT NULL DEFAULT 0;

-- Fill the columns of the workspaces that already have a record.
UPDATE inventory
SET
    last_release_lock_id = (SELECT w.last_release_lock_id FROM workspaces AS w WHERE w.id = inventory.unit_id),
    last_release_holder = (SELECT w.last_release_holder FROM workspaces AS w WHERE w.id = inventory.unit_id),
    last_release_by = (SELECT w.last_release_by FROM workspaces AS w WHERE w.id = inventory.unit_id),
    last_release_kind = (SELECT w.last_release_kind FROM workspaces AS w WHERE w.id = inventory.unit_id),
    last_release_at_ms = (SELECT w.last_release_at_ms FROM workspaces AS w WHERE w.id = inventory.unit_id)
WHERE tool = 'opentofu'
  AND EXISTS (SELECT 1 FROM workspaces AS w WHERE w.id = inventory.unit_id AND w.last_release_kind <> '');
