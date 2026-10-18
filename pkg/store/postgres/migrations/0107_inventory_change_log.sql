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

CREATE INDEX inventory_changes_unit ON inventory_changes USING btree (namespace_id, tool, unit_id, seq);
