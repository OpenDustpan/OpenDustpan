-- Child tables refer to the stable stack ID (ADR 19): add stack_id and fill
-- it. The adapters switch to stack_id before the copied columns are dropped.
-- SQLite cannot change a column to NOT NULL without rebuilding the table, so
-- the column arrives NOT NULL with an empty default and the update below
-- fills every row, as in 0003.
ALTER TABLE updates ADD COLUMN stack_id TEXT NOT NULL DEFAULT '';

UPDATE updates SET stack_id = (
    SELECT id FROM stacks
    WHERE stacks.namespace = updates.namespace
      AND stacks.project = updates.project
      AND stacks.name = updates.stack
) WHERE stack_id = '';

CREATE UNIQUE INDEX updates_stack_id ON updates (stack_id, id);

ALTER TABLE checkpoints ADD COLUMN stack_id TEXT NOT NULL DEFAULT '';

UPDATE checkpoints SET stack_id = (
    SELECT id FROM stacks
    WHERE stacks.namespace = checkpoints.namespace
      AND stacks.project = checkpoints.project
      AND stacks.name = checkpoints.stack
) WHERE stack_id = '';

CREATE UNIQUE INDEX checkpoints_stack_id ON checkpoints (stack_id);

ALTER TABLE update_events ADD COLUMN stack_id TEXT NOT NULL DEFAULT '';

UPDATE update_events SET stack_id = (
    SELECT id FROM stacks
    WHERE stacks.namespace = update_events.namespace
      AND stacks.project = update_events.project
      AND stacks.name = update_events.stack
) WHERE stack_id = '';

CREATE INDEX update_events_stack_id ON update_events (stack_id, update_id, sequence);
