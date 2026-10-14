-- The stable ID of the unit that an audit entry names (ADR 47), so the
-- history of one unit can be found across a rename or a reuse of its name.
-- An empty value means that the object is not a unit or that the ID is not
-- known, as for every entry written before this migration.
ALTER TABLE audit_entries ADD COLUMN object_id TEXT NOT NULL DEFAULT '';

-- The purged units whose blobs may still exist (ADR 47). A purge inserts a
-- row in its fenced transaction and deletes the rows of the unit. The
-- caller then deletes the blobs of the unit and keeps the row, because a
-- late write can still leave a blob. Only the sweep (#530) removes the row,
-- after it deletes the blobs again, so no blob of a purged unit outlives it
-- for good.
CREATE TABLE blob_purges (
    tool          TEXT   NOT NULL,
    unit_id       TEXT   NOT NULL,
    namespace     TEXT   NOT NULL,
    purged_at_ms  BIGINT NOT NULL,
    PRIMARY KEY (tool, unit_id)
);

CREATE INDEX blob_purges_age ON blob_purges (purged_at_ms, tool, unit_id);
