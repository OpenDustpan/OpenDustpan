-- Durable schedules: periodic jobs that one instance at a time runs.
--
-- durable_schedules holds one row for each schedule. next_run_at_ms is the
-- time on the database clock from which the next tick is due. A claim of a
-- tick moves it to the time of the claim plus interval_ms with one
-- conditional update, so the ticks that no instance ran collapse into one,
-- and of several concurrent claims at most one succeeds. last_claimed_at_ms
-- is the time of the last claim, or NULL before the first claim.
CREATE TABLE durable_schedules (
    name               TEXT    NOT NULL PRIMARY KEY,
    interval_ms        BIGINT  NOT NULL CHECK (interval_ms > 0),
    next_run_at_ms     BIGINT  NOT NULL,
    last_claimed_at_ms BIGINT
);
