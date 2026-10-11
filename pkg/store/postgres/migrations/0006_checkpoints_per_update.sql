-- Checkpoints: one row for each mutating update (ADR 24). The key is the
-- stack ID and the update. Each write of the running update overwrites the
-- row of that update. The latest state of a stack is the checkpoint of its
-- highest version. The final delete of a stack deletes its checkpoints, so
-- every child foreign key deletes with its parent.
ALTER TABLE update_events DROP CONSTRAINT update_events_update_fkey;

ALTER TABLE checkpoints DROP CONSTRAINT checkpoints_update_fkey;

ALTER TABLE updates DROP CONSTRAINT updates_stack_id_fkey;

ALTER TABLE checkpoints DROP CONSTRAINT checkpoints_stack_id_fkey;

ALTER TABLE update_events DROP CONSTRAINT update_events_stack_id_fkey;

ALTER TABLE checkpoints DROP CONSTRAINT checkpoints_pkey;

ALTER TABLE checkpoints ADD PRIMARY KEY (stack_id, update_id);

ALTER TABLE updates ADD CONSTRAINT updates_stack_id_fkey
    FOREIGN KEY (stack_id) REFERENCES stacks (id) ON DELETE CASCADE;

ALTER TABLE checkpoints ADD CONSTRAINT checkpoints_stack_id_fkey
    FOREIGN KEY (stack_id) REFERENCES stacks (id) ON DELETE CASCADE;

ALTER TABLE checkpoints ADD CONSTRAINT checkpoints_update_fkey
    FOREIGN KEY (stack_id, update_id) REFERENCES updates (stack_id, id) ON DELETE CASCADE;

ALTER TABLE update_events ADD CONSTRAINT update_events_stack_id_fkey
    FOREIGN KEY (stack_id) REFERENCES stacks (id) ON DELETE CASCADE;

ALTER TABLE update_events ADD CONSTRAINT update_events_update_fkey
    FOREIGN KEY (stack_id, update_id) REFERENCES updates (stack_id, id) ON DELETE CASCADE;
