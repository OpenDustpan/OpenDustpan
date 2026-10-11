-- Child tables move to the stack ID: drop the copied namespace, project and
-- stack columns. A rename moves no rows.
DROP INDEX IF EXISTS updates_one_running;

DROP INDEX IF EXISTS updates_history;

DROP INDEX IF EXISTS updates_stack_id;

DROP INDEX IF EXISTS checkpoints_stack_id;

DROP INDEX IF EXISTS update_events_stack_id;

ALTER TABLE update_events DROP CONSTRAINT update_events_namespace_project_stack_update_id_fkey;

ALTER TABLE checkpoints DROP CONSTRAINT checkpoints_namespace_project_stack_update_id_fkey;

ALTER TABLE updates DROP CONSTRAINT updates_namespace_project_stack_fkey;

ALTER TABLE update_events DROP CONSTRAINT update_events_pkey;

ALTER TABLE checkpoints DROP CONSTRAINT checkpoints_pkey;

ALTER TABLE updates DROP CONSTRAINT updates_pkey;

ALTER TABLE updates ADD PRIMARY KEY (stack_id, id);

ALTER TABLE checkpoints ADD PRIMARY KEY (stack_id);

ALTER TABLE update_events ADD PRIMARY KEY (stack_id, update_id, sequence);

ALTER TABLE checkpoints ADD CONSTRAINT checkpoints_update_fkey
    FOREIGN KEY (stack_id, update_id) REFERENCES updates (stack_id, id);

ALTER TABLE update_events ADD CONSTRAINT update_events_update_fkey
    FOREIGN KEY (stack_id, update_id) REFERENCES updates (stack_id, id);

ALTER TABLE updates DROP COLUMN namespace;

ALTER TABLE updates DROP COLUMN project;

ALTER TABLE updates DROP COLUMN stack;

ALTER TABLE checkpoints DROP COLUMN namespace;

ALTER TABLE checkpoints DROP COLUMN project;

ALTER TABLE checkpoints DROP COLUMN stack;

ALTER TABLE update_events DROP COLUMN namespace;

ALTER TABLE update_events DROP COLUMN project;

ALTER TABLE update_events DROP COLUMN stack;

CREATE UNIQUE INDEX updates_one_running ON updates (stack_id)
WHERE status = 'running' AND NOT dry_run AND kind <> 'preview';

CREATE INDEX updates_history ON updates (stack_id, version)
WHERE status <> 'not started' AND NOT dry_run AND kind <> 'preview';
