-- The source filter of each namespace: the submodules and the Git LFS
-- objects that a source snapshot may hold, the switch that fails a fetch on a
-- denied one, and the protected branches. The column holds one JSON
-- document, written by the store. The empty text is the empty filter, so a
-- namespace that never set it has no submodule, no Git LFS object and no
-- protected branch. The store checks the document at each write and at each
-- read, and the CHECK refuses a row that holds a text that is not JSON or a
-- text above 1 MiB.
ALTER TABLE namespaces ADD COLUMN source_filter TEXT NOT NULL DEFAULT ''
    CHECK (length(CAST(source_filter AS BLOB)) <= 1048576 AND (source_filter = '' OR json_valid(source_filter)));
