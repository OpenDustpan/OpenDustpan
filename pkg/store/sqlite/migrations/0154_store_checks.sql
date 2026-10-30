-- The store checks of the installation: one row for each deep check of a blob
-- store. A row holds the target ("blobs" or "backup"), the state, who asked
-- and the times. The report is the JSON of the result, and the error is the
-- fixed text of a check that did not run to its end. The restore keeps the
-- live rows of this table as it keeps the other control tables, so a running
-- check stays readable. A backup leaves the table out.
--
-- The unique index allows one running check for each installation. The CHECK
-- constraints are a defense-in-depth layer. The service validates the ID,
-- the target and the state before it writes, without relying on them.
CREATE TABLE store_checks (
    id            TEXT    NOT NULL PRIMARY KEY CHECK (length(id) BETWEEN 5 AND 40),
    target        TEXT    NOT NULL CHECK (target IN ('blobs', 'backup')),
    state         TEXT    NOT NULL CHECK (state IN ('running', 'succeeded', 'failed')),
    requested_by  TEXT    NOT NULL,
    started_at_ms INTEGER NOT NULL,
    ended_at_ms   INTEGER NOT NULL DEFAULT 0,
    error         TEXT    NOT NULL DEFAULT '',
    report        TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE UNIQUE INDEX store_checks_one_running ON store_checks ((state = 'running')) WHERE state = 'running';

CREATE INDEX store_checks_target_ended ON store_checks (target, ended_at_ms);

CREATE INDEX store_checks_started_at_ms ON store_checks (started_at_ms DESC);
