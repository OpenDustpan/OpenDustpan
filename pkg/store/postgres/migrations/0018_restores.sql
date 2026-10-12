-- Restores (ADR 38 decisions 8 and 9): one row for each restore that this
-- installation runs. Rows carry no fence: they describe the installation,
-- not one namespace. A server that starts marks every running row failed.
-- ended_at_ms stays NULL while the restore runs; the adapter decodes NULL
-- as the zero time. pre_backup_id stays empty when the restore stopped
-- before the apply.
CREATE TABLE restores (
    id            TEXT   NOT NULL PRIMARY KEY,
    state         TEXT   NOT NULL,
    backup_id     TEXT   NOT NULL,
    pre_backup_id TEXT   NOT NULL DEFAULT '',
    requested_by  TEXT   NOT NULL DEFAULT '',
    started_at_ms BIGINT NOT NULL,
    ended_at_ms   BIGINT,
    error         TEXT   NOT NULL DEFAULT ''
);

CREATE INDEX restores_started_at_ms ON restores (started_at_ms DESC);
