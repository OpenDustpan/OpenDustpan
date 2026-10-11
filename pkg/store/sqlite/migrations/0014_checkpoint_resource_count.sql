-- Checkpoint resource count: the number of managed resources beyond the
-- automatic stack resource, stored with each checkpoint so the final delete
-- of a stack checks it without loading the deployment. The column stays
-- NULL for rows written before the count: a NULL count means not counted.
-- New checkpoints always store the count.
ALTER TABLE checkpoints ADD COLUMN resource_count INTEGER;
