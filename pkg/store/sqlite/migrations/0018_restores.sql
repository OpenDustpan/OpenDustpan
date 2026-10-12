-- Restores (ADR 38 decisions 8 and 9): one row for each restore that this
-- installation runs, newest first. The row keeps the ID, the state
-- (checking, applying, succeeded or failed), the backup ID it restores, the
-- backup taken before the apply (empty when the restore stopped before it),
-- who asked, the start and end times as Unix milliseconds, and the error
-- text for an operator. The end time is 0 while the restore runs. The row
-- survives a restart. A server that starts marks every running row failed.
-- The row carries no fence: it describes the installation, not one namespace.
CREATE TABLE restores (
    id            TEXT    NOT NULL PRIMARY KEY,
    state         TEXT    NOT NULL,
    backup_id     TEXT    NOT NULL,
    pre_backup_id TEXT    NOT NULL DEFAULT '',
    requested_by  TEXT    NOT NULL DEFAULT '',
    started_at_ms INTEGER NOT NULL,
    ended_at_ms   INTEGER NOT NULL DEFAULT 0,
    error         TEXT    NOT NULL DEFAULT ''
) STRICT;
