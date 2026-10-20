-- The journal of the writes that only reduce access (a sign-out, a session or
-- token revocation, the disable of a principal) and that ran while the
-- installation was in maintenance mode. A restore replaces the access tables
-- with the backup, so it would lose these writes. The restore applies the
-- journal again after the swap. The restore keeps the live rows of this table
-- as it keeps the other control tables. A backup leaves the table out.
-- Entering maintenance mode clears it and starts a new stay in maintenance mode.
CREATE TABLE maintenance_revocations (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT    NOT NULL CHECK (kind IN ('session', 'token', 'principal')),
    target     TEXT    NOT NULL,
    end_reason TEXT    NOT NULL,
    at_ms      INTEGER NOT NULL
);
