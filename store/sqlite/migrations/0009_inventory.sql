-- Inventory: the cross-tool read model of every unit in a namespace (ADR 13).
-- Each tool package publishes its own writes here in the same transaction as
-- the tool's own write. Nothing else writes to this table. The unit ID stays
-- with the unit through a rename (ADR 19); the core keeps it opaque.
CREATE TABLE inventory (
    namespace       TEXT NOT NULL REFERENCES namespaces (name),
    tool            TEXT NOT NULL,
    name            TEXT NOT NULL,
    unit_id         TEXT NOT NULL,
    display_name    TEXT NOT NULL,
    last_changed_ms INTEGER NOT NULL,
    status          TEXT NOT NULL DEFAULT '',
    resource_count  INTEGER,
    PRIMARY KEY (namespace, tool, name)
) STRICT;

CREATE INDEX inventory_unit_id ON inventory (unit_id);

-- Existing stacks become Pulumi entries: the tool's own name is
-- project/stack, the display name matches it, the last change is the stack's
-- last update, the status is unknown and the resource count is unknown.
INSERT INTO inventory (namespace, tool, name, unit_id, display_name, last_changed_ms, status, resource_count)
SELECT namespace, 'pulumi', project || '/' || name, id, project || '/' || name, updated_at_ms, '', NULL
FROM stacks;
