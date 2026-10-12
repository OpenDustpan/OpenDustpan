-- Audit value count: the count of secret values that one decrypt call
-- opened. The column stays NULL for rows written before the count and for
-- entries without a count: a NULL count means no count. Decrypt entries
-- store the count and leave version empty.
ALTER TABLE audit_entries ADD COLUMN value_count BIGINT;
