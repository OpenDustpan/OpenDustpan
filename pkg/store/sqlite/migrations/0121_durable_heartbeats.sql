-- The heartbeat of the workers of durable operations: one row for each owner,
-- with the time of its latest claim or renewal on the clock of the store.
-- The rows describe the running workers, not the data of the installation. A
-- backup leaves the table out and a restore leaves it empty. The next beat of
-- a worker makes its row again.
CREATE TABLE durable_heartbeats (
    owner TEXT    NOT NULL PRIMARY KEY,
    at_ms INTEGER NOT NULL
) STRICT;

CREATE INDEX durable_heartbeats_at ON durable_heartbeats (at_ms);
