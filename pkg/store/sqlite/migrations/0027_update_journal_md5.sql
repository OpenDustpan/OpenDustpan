-- Journal blob MD5: the MD5 of the stored bytes of each batch, so the backup names
-- a journal blob the way it names a checkpoint blob (its MD5 and size). SQLite cannot
-- add a NOT NULL column without a default, so the table is rebuilt. The rebuild
-- refuses to run when rows exist: the MD5 of their bytes is not known in SQL. No
-- production row exists before item 9, so no row is refused in production.
CREATE TABLE update_journal_md5_guard (
    rows INTEGER NOT NULL,
    CONSTRAINT update_journal_holds_no_rows CHECK (rows = 0)
) STRICT;

INSERT INTO update_journal_md5_guard (rows) SELECT COUNT(*) FROM update_journal;

DROP TABLE update_journal_md5_guard;

CREATE TABLE update_journal_new (
    stack_id        TEXT    NOT NULL,
    update_id       TEXT    NOT NULL,
    arrival         INTEGER NOT NULL,
    blob_key        TEXT    NOT NULL,
    sha256          BLOB    NOT NULL,
    md5             BLOB    NOT NULL,
    size            INTEGER NOT NULL,
    lowest_sequence INTEGER NOT NULL,
    sequence_ids    TEXT    NOT NULL,
    operation_ids   TEXT    NOT NULL,
    PRIMARY KEY (stack_id, update_id, arrival),
    FOREIGN KEY (stack_id, update_id) REFERENCES updates (stack_id, id) ON DELETE CASCADE
) STRICT;

INSERT INTO update_journal_new (stack_id, update_id, arrival, blob_key, sha256, md5, size,
                                lowest_sequence, sequence_ids, operation_ids)
SELECT stack_id, update_id, arrival, blob_key, sha256, X'', size,
       lowest_sequence, sequence_ids, operation_ids
FROM update_journal;

DROP TABLE update_journal;

ALTER TABLE update_journal_new RENAME TO update_journal;
