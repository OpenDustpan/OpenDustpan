-- Statements that run after the old migrations and before the dump: the
-- seed rows and the drops that only the baseline makes.
INSERT INTO audit_chain_state (id, state, changed_at_ms, changed_by) VALUES (1, 'on', 0, '');
ALTER TABLE checkpoints DROP COLUMN resource_count;
