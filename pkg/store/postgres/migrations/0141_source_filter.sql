-- The source filter of each namespace: the submodules and the Git LFS
-- objects that a source snapshot may hold, the switch that fails a fetch on a
-- denied one, and the protected branches. The column holds one JSON
-- document, written by the store. The empty text is the empty filter, so a
-- namespace that never set it has no submodule, no Git LFS object and no
-- protected branch. The store checks the document at each write and at each
-- read, and the CHECK refuses a row that holds a text that is not JSON or a
-- text above 1 MiB.
ALTER TABLE namespaces ADD COLUMN source_filter text NOT NULL DEFAULT ''
    CONSTRAINT namespaces_source_filter_check
    CHECK (octet_length(source_filter) <= 1048576 AND (source_filter = '' OR pg_input_is_valid(source_filter, 'jsonb')));
