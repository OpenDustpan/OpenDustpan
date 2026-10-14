-- The access token that started an update (ADR 53). A running update checks
-- this token on each lease request, so a revoke, an expiry or a deleted
-- principal ends the update's writes at once. Old rows and updates that a
-- bootstrap administrator token started keep an empty ID, and skip the check.
ALTER TABLE updates ADD COLUMN token_id TEXT NOT NULL DEFAULT '';
