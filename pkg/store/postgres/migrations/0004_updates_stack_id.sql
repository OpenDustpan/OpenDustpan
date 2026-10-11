-- Child tables refer to the stable stack ID (ADR 19): add stack_id and fill
-- it. The adapters switch to stack_id before the copied columns are dropped.
ALTER TABLE updates ADD COLUMN stack_id TEXT;

UPDATE updates SET stack_id = stacks.id
FROM stacks
WHERE stacks.namespace = updates.namespace
  AND stacks.project = updates.project
  AND stacks.name = updates.stack;

ALTER TABLE updates ALTER COLUMN stack_id SET NOT NULL;

CREATE INDEX updates_stack_id ON updates (stack_id, id);

ALTER TABLE updates ADD CONSTRAINT updates_stack_id_fkey FOREIGN KEY (stack_id) REFERENCES stacks (id);

ALTER TABLE checkpoints ADD COLUMN stack_id TEXT;

UPDATE checkpoints SET stack_id = stacks.id
FROM stacks
WHERE stacks.namespace = checkpoints.namespace
  AND stacks.project = checkpoints.project
  AND stacks.name = checkpoints.stack;

ALTER TABLE checkpoints ALTER COLUMN stack_id SET NOT NULL;

CREATE UNIQUE INDEX checkpoints_stack_id ON checkpoints (stack_id);

ALTER TABLE checkpoints ADD CONSTRAINT checkpoints_stack_id_fkey FOREIGN KEY (stack_id) REFERENCES stacks (id);

ALTER TABLE update_events ADD COLUMN stack_id TEXT;

UPDATE update_events SET stack_id = stacks.id
FROM stacks
WHERE stacks.namespace = update_events.namespace
  AND stacks.project = update_events.project
  AND stacks.name = update_events.stack;

ALTER TABLE update_events ALTER COLUMN stack_id SET NOT NULL;

CREATE INDEX update_events_stack_id ON update_events (stack_id, update_id, sequence);

ALTER TABLE update_events ADD CONSTRAINT update_events_stack_id_fkey FOREIGN KEY (stack_id) REFERENCES stacks (id);
