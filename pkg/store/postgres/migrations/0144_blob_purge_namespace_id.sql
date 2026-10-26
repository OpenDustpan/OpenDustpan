-- A blob purge record names its namespace by the stable namespace ID, not by
-- the name. A name can come back for a new namespace after a delete, so a
-- delete of blobs must read the ID that the record holds. The name column is
-- dropped: nothing else reads it. One join by name fills the ID of each row
-- that exists now. A row whose namespace is gone keeps the empty ID, and the
-- retention sweep leaves such a record and logs it. No foreign key: the
-- record must outlive its namespace, so that the sweep can report it.
ALTER TABLE blob_purges ADD COLUMN namespace_id text NOT NULL DEFAULT '';

UPDATE blob_purges SET namespace_id = n.id
FROM namespaces AS n
WHERE n.name = blob_purges.namespace;

ALTER TABLE blob_purges ALTER COLUMN namespace_id DROP DEFAULT;

ALTER TABLE blob_purges DROP COLUMN namespace;
