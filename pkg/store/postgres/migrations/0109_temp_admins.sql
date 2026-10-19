-- Temporary administrators: one record for each principal that the host
-- command makes for recovery, with its fixed expiry and the reason that the
-- host operator gave. The expiry disables the principal, ends its grants
-- and its recovery tokens, and keeps the record as a tombstone, with an
-- audit entry. A delete of the principal removes the record.
CREATE TABLE temp_admins (
  principal_id TEXT PRIMARY KEY,
  subject TEXT NOT NULL,
  expires_at_ms BIGINT NOT NULL,
  created_at_ms BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  reason TEXT NOT NULL
);
