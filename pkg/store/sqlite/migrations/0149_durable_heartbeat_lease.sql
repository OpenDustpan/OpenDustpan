-- The lease period of the worker that wrote a heartbeat. An api process
-- counts a worker as absent after 2 lease periods of the worker without a
-- beat, so it reads the lease of the worker and not its own. A beat with
-- lease_ms 0 records no lease: a worker of an older build wrote it, and the
-- reader then uses its own lease. The rows describe the running workers, not
-- the data of the installation, so the table starts empty and the next beat of
-- each worker makes its row again.
DELETE FROM durable_heartbeats;

ALTER TABLE durable_heartbeats ADD COLUMN lease_ms INTEGER NOT NULL DEFAULT 0;
