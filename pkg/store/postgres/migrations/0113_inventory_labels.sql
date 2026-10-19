-- The labels of the units in the inventory: one row for each label of a live
-- unit, keyed by the stable unit ID, so a rename of the unit keeps its labels.
-- A tool writes its labels in the transaction of its own write: the Pulumi
-- CLI's stack tags are the labels of a stack. A soft delete and a purge of the
-- unit remove its rows.
CREATE TABLE inventory_labels (
    namespace   text NOT NULL REFERENCES namespaces (name),
    unit_id     text NOT NULL,
    label_key   text NOT NULL,
    label_value text NOT NULL,
    PRIMARY KEY (unit_id, label_key)
);

CREATE INDEX inventory_labels_namespace ON inventory_labels (namespace, unit_id);

-- A backfill: the tags of each live stack become its labels.
INSERT INTO inventory_labels (namespace, unit_id, label_key, label_value)
SELECT s.namespace, t.stack_id, t.name, t.value
FROM stack_tags AS t
JOIN stacks AS s ON s.id = t.stack_id
WHERE s.deleted_at_ms IS NULL;
