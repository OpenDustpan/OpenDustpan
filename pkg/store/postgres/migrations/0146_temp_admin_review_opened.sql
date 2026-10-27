-- A temporary administrator that ended needs a break-glass review. The
-- sweep marks the record when the review opened, so a sweep that finds an
-- ended record with no mark opens the review again. A record that ended
-- before this column keeps no mark, so the next sweep with a review store
-- opens its review.
ALTER TABLE temp_admins ADD COLUMN review_opened_at_ms BIGINT;
