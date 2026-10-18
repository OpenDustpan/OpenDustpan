-- The inventory change log: one row for each inventory write of a unit, with
-- a sequence that a counter row of the namespace assigns in the transaction of
-- the write. The rows and the counter hold the stable ID of the namespace,
-- never its name, and no foreign key, so a purge of a unit or of a namespace
-- keeps them. A namespace that a team creates again under the same name has a
-- new ID, so it reads none of them.
CREATE TABLE inventory_change_heads (
    namespace_id TEXT    NOT NULL PRIMARY KEY,
    last_seq     INTEGER NOT NULL
) STRICT;

CREATE TABLE inventory_changes (
    namespace_id   TEXT    NOT NULL,
    seq            INTEGER NOT NULL,
    at_ms          INTEGER NOT NULL,
    tool           TEXT    NOT NULL,
    unit_id        TEXT    NOT NULL,
    name           TEXT    NOT NULL,
    display_name   TEXT    NOT NULL,
    status         TEXT    NOT NULL,
    collection     TEXT    NOT NULL,
    kind           TEXT    NOT NULL,
    resource_count INTEGER,
    PRIMARY KEY (namespace_id, seq)
) STRICT;

CREATE INDEX inventory_changes_unit ON inventory_changes (namespace_id, tool, unit_id, seq);
