-- The IP allowlist of the namespace (ADR 55, decision 8): the list of CIDRs,
-- comma-separated. Empty means no rule.
ALTER TABLE namespaces ADD COLUMN ip_allowlist TEXT NOT NULL DEFAULT '';
