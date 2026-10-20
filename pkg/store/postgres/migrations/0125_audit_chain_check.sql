-- The last result of the scheduled audit chain check, and the mark of a
-- checkpoint mismatch: one row for the installation. The instance that runs
-- the check writes the row, and every instance reads it, so each instance
-- exports the same dustpan_audit_chain_invalid and a mismatch that one
-- instance finds stops signing on all instances. The row is a control table:
-- a backup leaves it out and a restore keeps the live row. A restore starts a
-- new checkpoint epoch, and a mark of an older epoch does not stop signing in
-- the new one. No row means no check ran yet and no mark. A zero in a *_ms
-- column means "not set".
CREATE TABLE audit_chain_check (
    id            smallint NOT NULL PRIMARY KEY,
    checked_at_ms bigint   NOT NULL,
    invalid       bigint   NOT NULL,
    marked        boolean  NOT NULL,
    mark_epoch    bigint   NOT NULL,
    mark_scope    text     NOT NULL,
    mark_sequence bigint   NOT NULL,
    marked_at_ms  bigint   NOT NULL,
    CHECK (id = 1),
    CHECK (invalid >= 0),
    CHECK (marked OR (mark_epoch = 0 AND mark_scope = '' AND mark_sequence = 0 AND marked_at_ms = 0))
);
