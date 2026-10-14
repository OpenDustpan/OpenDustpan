-- Soft delete of stacks (ADR 47): a delete keeps the stack with its updates,
-- checkpoints, events, tags and data key until the retention period ends.
-- The delete moves the name of the stack to deleted_name and sets the name to
-- "deleted:" and the stable ID. A stack name cannot hold ":", so the deleted
-- row never matches a lookup by name, and the name is free at once for a new
-- stack. deleted_at_ms is the time of the delete, and NULL for a live stack.
ALTER TABLE stacks ADD COLUMN deleted_at_ms BIGINT;
ALTER TABLE stacks ADD COLUMN deleted_name TEXT;

-- Deleted units in the inventory (ADR 13, ADR 47): the delete transaction
-- moves the entry of a unit from the live inventory to this table, and a
-- restore moves it back. The final delete removes the row. The live
-- inventory never holds a deleted unit, so a new unit can take the name at
-- once. The key is the tool and the unit ID, which no rename or delete
-- changes. name is the tool's own name of the unit at the time of the
-- delete. purge_after_ms is the end of the retention period.
CREATE TABLE inventory_deleted (
    namespace      TEXT    NOT NULL REFERENCES namespaces (name),
    tool           TEXT    NOT NULL,
    unit_id        TEXT    NOT NULL,
    name           TEXT    NOT NULL,
    deleted_at_ms  BIGINT NOT NULL,
    purge_after_ms BIGINT NOT NULL,
    PRIMARY KEY (tool, unit_id)
);

CREATE INDEX inventory_deleted_namespace ON inventory_deleted (namespace, tool, name);
