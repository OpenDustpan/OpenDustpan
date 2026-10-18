-- The inventory change log: one row for each inventory write of a unit, with
-- a sequence that a counter row of the namespace assigns in the transaction of
-- the write. The rows and the counter hold the stable ID of the namespace,
-- never its name, and no foreign key, so a purge of a unit or of a namespace
-- keeps them. A namespace that a team creates again under the same name has a
-- new ID, so it reads none of them.
CREATE TABLE inventory_change_heads (
    namespace_id text NOT NULL PRIMARY KEY,
    last_seq bigint NOT NULL
);

CREATE TABLE inventory_changes (
    namespace_id text NOT NULL,
    seq bigint NOT NULL,
    at_ms bigint NOT NULL,
    tool text NOT NULL,
    unit_id text NOT NULL,
    name text NOT NULL,
    display_name text NOT NULL,
    status text NOT NULL,
    collection text NOT NULL,
    kind text NOT NULL,
    resource_count bigint,
    PRIMARY KEY (namespace_id, seq)
);

-- The index serves the skip scan over the units of a namespace and the two
-- lookups of the highest sequence of a unit, one of them limited by time.
CREATE INDEX inventory_changes_unit ON inventory_changes USING btree (namespace_id, tool, unit_id, seq)
    INCLUDE (at_ms);

-- A baseline: one row for each unit that exists now, so that the log is
-- complete from this migration onward. A live unit gets a created row at its
-- last change time. A soft-deleted unit gets a deleted row at its delete time.
-- The sequence counts from 1 in each namespace, in the order of the times.
INSERT INTO inventory_changes (
    namespace_id, seq, at_ms, tool, unit_id, name, display_name, status, collection, kind, resource_count
)
SELECT n.id,
       row_number() OVER (PARTITION BY n.id ORDER BY b.at_ms, b.tool, b.unit_id),
       b.at_ms, b.tool, b.unit_id, b.name, b.display_name, b.status, '', b.kind, b.resource_count
FROM (
    SELECT namespace, last_changed_ms AS at_ms, tool, unit_id, name, display_name, status,
           'created' AS kind, resource_count
    FROM inventory
    UNION ALL
    SELECT namespace, deleted_at_ms, tool, unit_id, name, name, '', 'deleted', NULL
    FROM inventory_deleted
) AS b
JOIN namespaces AS n ON n.name = b.namespace;

INSERT INTO inventory_change_heads (namespace_id, last_seq)
SELECT namespace_id, MAX(seq) FROM inventory_changes GROUP BY namespace_id;
