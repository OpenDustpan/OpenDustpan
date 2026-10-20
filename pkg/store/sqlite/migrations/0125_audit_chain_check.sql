-- The last result of the scheduled audit chain check: one row for the
-- installation. The instance that runs the check writes the row, and every
-- instance reads it, so each instance exports the same
-- dustpan_audit_chain_invalid. No row means no check ran yet. A zero in a
-- *_ms column means "not set". The row is a control table: a backup leaves
-- it out and a restore keeps the live row.
CREATE TABLE audit_chain_check (
    id            INTEGER NOT NULL PRIMARY KEY,
    checked_at_ms INTEGER NOT NULL,
    invalid       INTEGER NOT NULL,
    CHECK (id = 1),
    CHECK (invalid >= 0)
) STRICT;

-- The mark of a checkpoint mismatch: one row for each scope. A mismatch that
-- one instance finds in a scope stops signing of that scope on every
-- instance. The first mark of a scope in an epoch stays, and a mark of a
-- newer epoch replaces it. A restore starts a new checkpoint epoch, and a
-- mark of an older epoch does not stop signing in the new one. The table is a
-- control table: a backup leaves it out and a restore keeps the live rows.
CREATE TABLE audit_chain_mark (
    scope        TEXT    NOT NULL PRIMARY KEY,
    epoch        INTEGER NOT NULL,
    sequence     INTEGER NOT NULL,
    marked_at_ms INTEGER NOT NULL,
    CHECK (scope <> ''),
    CHECK (epoch >= 0),
    CHECK (sequence >= 0)
) STRICT;
