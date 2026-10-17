-- Statements that run after the old migrations and before the dump: the
-- seed rows and the drops that only the baseline makes.
INSERT INTO audit_chain_state (id, state, changed_at_ms, changed_by) VALUES (1, 'on', 0, '');
ALTER TABLE checkpoints DROP COLUMN resource_count;
-- SQLite cannot make a column NOT NULL in place, so checkpoints is built
-- again without deployment and with blob_key NOT NULL. The table is empty.
CREATE TABLE checkpoints_new (
    stack_id       TEXT    NOT NULL REFERENCES stacks (id) ON DELETE CASCADE,
    update_id      TEXT    NOT NULL,
    lease_digest   BLOB    NOT NULL,
    schema_version INTEGER NOT NULL,
    sequence       INTEGER NOT NULL,
    digest         BLOB    NOT NULL,
    invalid        INTEGER NOT NULL,
    features_json  TEXT    NOT NULL,
    saved_at_ms    INTEGER NOT NULL,
    blob_key       TEXT    NOT NULL CHECK (blob_key <> ''),
    size           INTEGER,
    blob_size      INTEGER,
    blob_md5       BLOB,
    PRIMARY KEY (stack_id, update_id),
    FOREIGN KEY (stack_id) REFERENCES stacks (id) ON DELETE CASCADE,
    FOREIGN KEY (stack_id, update_id) REFERENCES updates (stack_id, id) ON DELETE CASCADE
) STRICT;
DROP TABLE checkpoints;
ALTER TABLE checkpoints_new RENAME TO checkpoints;
