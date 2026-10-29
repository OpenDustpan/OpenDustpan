-- The version row of each running server process: the instance ID, the binary
-- version and the start time. A process writes its row at start-up and deletes
-- it at a clean stop. The row is not a liveness lease: nothing renews it, and
-- nothing reads it to decide that an instance is live. The installation API
-- lists the rows, and an administrator deletes the row of a process that died.
-- A contract migration that raises the minimum binary version does not run
-- while a row below the new minimum exists.
-- The restore keeps the live rows of this table as it keeps the other control
-- tables. A backup leaves the table out.
--
-- The CHECK constraints are a defense-in-depth layer. The store validates the
-- ID and the version before it writes, without relying on them.
CREATE TABLE instance_versions (
    id             TEXT    PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 64),
    binary_version TEXT    NOT NULL CHECK (length(binary_version) BETWEEN 1 AND 64),
    started_at_ms  INTEGER NOT NULL
) STRICT;
