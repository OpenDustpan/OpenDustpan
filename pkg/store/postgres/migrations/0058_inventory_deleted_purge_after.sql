-- An index for the retention sweep: it reads the deleted units of
-- every namespace whose retention has ended, the oldest end first. The
-- sweep orders tool and unit_id in byte order (COLLATE "C"), so the index
-- uses the same collation and a page reads in index order without a sort,
-- whatever the collation of the database.
CREATE INDEX inventory_deleted_purge_after
    ON inventory_deleted (purge_after_ms, tool COLLATE "C", unit_id COLLATE "C");
