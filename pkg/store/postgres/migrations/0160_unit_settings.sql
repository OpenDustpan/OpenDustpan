-- Unit settings: the code location, the executor, the runner pool, the
-- write-back options and the custom steps of one unit. A unit has at most one
-- row. unit_id is the stable ID of the unit, so a rename keeps the row, and the
-- row has no foreign key to the inventory, as jobs have none; the purge of a
-- unit deletes it in the same transaction. body holds the JSON document of the
-- settings that no query reads. version counts the writes of the row from 1.
-- A write names the version that the caller read.
CREATE TABLE unit_settings (
    unit_id       text   NOT NULL PRIMARY KEY,
    namespace_id  text   NOT NULL REFERENCES namespaces (id),
    version       bigint NOT NULL CHECK (version >= 1),
    body          text   NOT NULL,
    updated_at_ms bigint NOT NULL
);

CREATE INDEX unit_settings_namespace ON unit_settings (namespace_id);

-- The unit defaults of a namespace: the runner pool for jobs of a unit that
-- sets none. A namespace has at most one row, and a namespace with no row has
-- no default.
CREATE TABLE unit_defaults (
    namespace_id  text   NOT NULL PRIMARY KEY REFERENCES namespaces (id),
    pool          text   NOT NULL,
    version       bigint NOT NULL CHECK (version >= 1),
    updated_at_ms bigint NOT NULL
);
