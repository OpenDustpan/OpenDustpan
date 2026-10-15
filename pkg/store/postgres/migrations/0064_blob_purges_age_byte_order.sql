-- The blob purge list orders tool and unit_id in byte order (COLLATE "C").
-- The earlier index uses the collation of the database, so on a database
-- whose collation is not C, each page of the list sorted all its rows. This
-- index uses the same collation as the list, so a page reads in index order
-- without a sort, whatever the collation of the database.
DROP INDEX blob_purges_age;
CREATE INDEX blob_purges_age
    ON blob_purges (purged_at_ms, tool COLLATE "C", unit_id COLLATE "C");
