-- The latest drift result of each unit. A unit has at most one row: a newer
-- result replaces it. The row holds the stable unit ID and the tool, because a
-- result outlives a rename of the unit, and it has no foreign key to the
-- inventory, as jobs have none. status is drifted or in-sync: a unit with no
-- row has the status unknown. job_id is the drift check job that made the
-- result, and one job makes at most one result. resources_json holds the
-- addresses and kinds of the differing resources, at most 1000, and no value.
CREATE TABLE drift_results (
    unit_id        TEXT    NOT NULL PRIMARY KEY,
    namespace_id   TEXT    NOT NULL REFERENCES namespaces (id),
    tool           TEXT    NOT NULL,
    job_id         TEXT    NOT NULL,
    status         TEXT    NOT NULL CHECK (status IN ('drifted', 'in-sync')),
    checked_at_ms  INTEGER NOT NULL,
    truncated      INTEGER NOT NULL CHECK (truncated IN (0, 1)),
    resources_json TEXT    NOT NULL
) STRICT;

CREATE UNIQUE INDEX drift_results_job ON drift_results (job_id);
CREATE INDEX drift_results_namespace ON drift_results (namespace_id);
