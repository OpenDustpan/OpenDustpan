-- Backups (ADR 30 decision 9): one row for each backup that this
-- installation made, newest first. The row keeps the ID, the state
-- (running, succeeded or failed), the start and end times as Unix
-- milliseconds, the target (for example "dir:/var/backups") and the error
-- text for an operator. The end time is 0 while the backup runs. The row
-- survives a restart. A server that starts marks every running row failed.
-- The row carries no fence: it describes the installation, not one namespace.
CREATE TABLE backups (
    id            TEXT    NOT NULL PRIMARY KEY,
    state         TEXT    NOT NULL,
    started_at_ms INTEGER NOT NULL,
    ended_at_ms   INTEGER NOT NULL DEFAULT 0,
    target        TEXT    NOT NULL DEFAULT '',
    error         TEXT    NOT NULL DEFAULT ''
) STRICT;
