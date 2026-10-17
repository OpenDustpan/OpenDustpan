-- Temporary administrators: one record for each principal that the host
-- command makes for recovery, with its fixed expiry. The expiry sweep
-- disables the principal and drops the record, with an audit entry for each
-- step.
CREATE TABLE temp_admins (
  principal_id TEXT PRIMARY KEY,
  subject TEXT NOT NULL,
  expires_at_ms BIGINT NOT NULL,
  created_at_ms BIGINT NOT NULL,
  created_by TEXT NOT NULL
);
