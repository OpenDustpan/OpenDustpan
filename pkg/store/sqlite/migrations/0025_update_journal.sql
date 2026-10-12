-- Update journal: each batch that a journaled update sends (ADR 43, decision
-- 6). The blob holds the batch body under blob_key. The row names that blob,
-- with the SHA-256 and the size of the stored bytes, the sequence IDs and the
-- operation IDs of the batch, and the arrival number of the update. Replay reads
-- the rows by arrival number. update_journal_sequences holds one row for each
-- sequence ID, so a retry and a conflict are found by key, not by a scan.
CREATE TABLE update_journal (
    stack_id        TEXT    NOT NULL,
    update_id       TEXT    NOT NULL,
    arrival         INTEGER NOT NULL,
    blob_key        TEXT    NOT NULL,
    sha256          BLOB    NOT NULL,
    size            INTEGER NOT NULL,
    lowest_sequence INTEGER NOT NULL,
    sequence_ids    TEXT    NOT NULL,
    operation_ids   TEXT    NOT NULL,
    PRIMARY KEY (stack_id, update_id, arrival),
    FOREIGN KEY (stack_id, update_id) REFERENCES updates (stack_id, id) ON DELETE CASCADE
) STRICT;

CREATE TABLE update_journal_sequences (
    stack_id  TEXT    NOT NULL,
    update_id TEXT    NOT NULL,
    sequence  INTEGER NOT NULL,
    arrival   INTEGER NOT NULL,
    sha256    BLOB    NOT NULL,
    PRIMARY KEY (stack_id, update_id, sequence),
    FOREIGN KEY (stack_id, update_id, arrival) REFERENCES update_journal (stack_id, update_id, arrival) ON DELETE CASCADE
) STRICT;
