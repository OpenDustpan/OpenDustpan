-- An index for the retention sweep: it reads the deleted units of
-- every namespace whose retention has ended, the oldest end first.
CREATE INDEX inventory_deleted_purge_after ON inventory_deleted (purge_after_ms, tool, unit_id);
