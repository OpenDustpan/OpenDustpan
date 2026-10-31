-- The commit rows of the upload versions of an import. The row is the commit
-- point of a version: the version exists when its row exists, and the row
-- names the facts file of the attempt that won. The primary key lets one
-- attempt win a sequence, also on a blob store that replaces an object
-- instead of refusing a second write. The row has no foreign key to the
-- import: the purge of an import record deletes its rows in the same
-- transaction, and the cancel or the release of the import deletes the
-- objects of the losing attempts.
CREATE TABLE import_upload_commits (
    namespace_id    text   NOT NULL,
    import_id       text   NOT NULL,
    unit_dir        text   NOT NULL,
    sequence        bigint NOT NULL,
    facts           text   NOT NULL,
    committed_at_ms bigint NOT NULL,
    PRIMARY KEY (namespace_id, import_id, unit_dir, sequence),
    CHECK (sequence >= 1)
);
