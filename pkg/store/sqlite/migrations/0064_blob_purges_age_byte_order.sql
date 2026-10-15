-- The PostgreSQL migration of this number recreates blob_purges_age with
-- byte order on tool and unit_id. SQLite compares text in byte order
-- already, so the index keeps its columns. It is recreated here so that
-- both dialects keep the same list of migrations.
DROP INDEX blob_purges_age;
CREATE INDEX blob_purges_age ON blob_purges (purged_at_ms, tool, unit_id);
