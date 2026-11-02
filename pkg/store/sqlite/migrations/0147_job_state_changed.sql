-- The time of the last change of the state of a job. updated_at_ms also moves
-- for a write that keeps the state, so the queue, dispatch and state timeouts
-- of the reconcile read this column. A job that exists keeps the time of its
-- last write as the best known time of its state.
ALTER TABLE jobs ADD COLUMN state_changed_at_ms INTEGER NOT NULL DEFAULT 0;

UPDATE jobs SET state_changed_at_ms = updated_at_ms;
