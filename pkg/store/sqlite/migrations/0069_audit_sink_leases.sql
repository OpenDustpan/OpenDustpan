-- Audit sink leases: one row per log sink that names the instance that
-- ships to the sink and the time the lease ends in Unix milliseconds. Only
-- the holder ships to the sink, so two instances never ship one entry twice.
-- The sink cursor stays the source of truth: a new holder goes on from the
-- cursor. A lease ends at its expiry, and any instance can take an expired
-- lease or a lease with no row. The table holds no foreign key.
CREATE TABLE audit_sink_leases (
    sink          TEXT    NOT NULL PRIMARY KEY,
    holder        TEXT    NOT NULL,
    expires_at_ms INTEGER NOT NULL
) STRICT;
