-- The store checks of the installation: one row for each deep check of a blob
-- store. A row holds the target ("blobs" or "backup"), the state, who asked
-- and the times. The report is the JSON of the result, and the error is the
-- fixed text of a check that did not run to its end. A restore keeps the live
-- rows, so a running check stays readable. A backup leaves the table out.
--
-- The unique index allows one running check for each installation. The CHECK
-- constraints are a defense-in-depth layer. The service validates the ID,
-- the target and the state before it writes, without relying on them.
CREATE TABLE store_checks (
    id            text   PRIMARY KEY,
    target        text   NOT NULL,
    state         text   NOT NULL,
    requested_by  text   NOT NULL,
    started_at_ms bigint NOT NULL,
    ended_at_ms   bigint,
    error         text   NOT NULL DEFAULT '',
    report        text   NOT NULL DEFAULT '',
    CONSTRAINT store_checks_id CHECK (length(id) BETWEEN 5 AND 40),
    CONSTRAINT store_checks_target CHECK (target IN ('blobs', 'backup')),
    CONSTRAINT store_checks_state CHECK (state IN ('running', 'succeeded', 'failed'))
);

CREATE UNIQUE INDEX store_checks_one_running ON store_checks ((state = 'running')) WHERE state = 'running';

CREATE INDEX store_checks_target_ended ON store_checks (target, ended_at_ms);

CREATE INDEX store_checks_started_at_ms ON store_checks (started_at_ms DESC);

DO $$
DECLARE
  live text := current_schema();
  ctl text := current_schema() || '_control';
BEGIN
  EXECUTE format('CREATE SCHEMA IF NOT EXISTS %I', ctl);
  EXECUTE format('ALTER TABLE %I.store_checks SET SCHEMA %I', live, ctl);
END $$;
