-- Run triggers belong to a namespace and live in a backup. A trigger starts a
-- plan of one unit. The row holds the stable ID of the namespace. A unit is
-- named by its tool and its stable unit ID, and a trigger outlives its unit
-- until the purge of the unit removes it, so there is no foreign key to the
-- inventory. The columns preset and cron hold the frequency of a schedule
-- trigger, and the source columns hold the unit that a follows trigger
-- follows. Both groups are empty strings for the other kinds. A source names
-- its namespace on its own: the rules refuse a source of another namespace, and
-- only a row of another version can state such a value, so a read can refuse
-- it. last_saver_subject is the subject of the principal that saved the
-- trigger last, and last_saver_id is the ID of that principal's record. The ID
-- tells a principal from a later principal that reuses the subject. The flag
-- enabled is 0 or 1.
CREATE TABLE triggers (
    namespace_id       TEXT    NOT NULL REFERENCES namespaces (id),
    id                 TEXT    NOT NULL,
    unit_tool          TEXT    NOT NULL,
    unit_id            TEXT    NOT NULL,
    kind               TEXT    NOT NULL CHECK (kind IN ('review-request-push', 'schedule', 'follows')),
    preset             TEXT    NOT NULL,
    cron               TEXT    NOT NULL,
    source_namespace   TEXT    NOT NULL,
    source_tool        TEXT    NOT NULL,
    source_unit_id     TEXT    NOT NULL,
    enabled            INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    last_saver_subject TEXT    NOT NULL,
    last_saver_id      TEXT    NOT NULL,
    created_at_ms      INTEGER NOT NULL,
    updated_at_ms      INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, id)
) STRICT;

-- The list of the triggers of one unit.
CREATE INDEX triggers_unit ON triggers (namespace_id, unit_tool, unit_id);

-- The enabled follows triggers of a source, and of a whole namespace for the
-- check of loops.
CREATE INDEX triggers_source ON triggers (namespace_id, source_tool, source_unit_id)
    WHERE kind = 'follows' AND enabled = 1;
