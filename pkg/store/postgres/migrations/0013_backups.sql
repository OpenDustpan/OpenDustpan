-- Backups: one row for each backup this installation made (ADR 30 decision 9).
-- Rows carry no fence: they describe the installation, not one namespace. A
-- server that starts marks every running row failed. ended_at_ms stays NULL
-- while the backup runs; the adapter decodes NULL as the zero time.
CREATE TABLE backups (
    id            TEXT   NOT NULL PRIMARY KEY,
    state         TEXT   NOT NULL,
    started_at_ms BIGINT NOT NULL,
    ended_at_ms   BIGINT,
    target        TEXT   NOT NULL,
    error         TEXT   NOT NULL DEFAULT ''
);

CREATE INDEX backups_started_at_ms ON backups (started_at_ms DESC);
