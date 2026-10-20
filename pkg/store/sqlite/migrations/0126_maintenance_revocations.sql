-- The journal of the writes that only reduce access (a sign-out, a session or
-- token revocation, the disable of a principal or of an identity provider,
-- the expiry of a temporary administrator, the revocation of a grant or of a
-- join token) and that ran while the installation was in maintenance mode. A
-- restore replaces the access tables with the backup, so it would lose these
-- writes. The restore applies the journal again after the swap, and writes one
-- audit entry for each row that it applies. The restore keeps the live rows of this
-- table as it keeps the other control tables. A backup leaves the table out.
-- Entering maintenance mode clears the table and starts a new stay in
-- maintenance mode.
--
-- The CHECK on kind is a defense-in-depth layer. The store writes only typed
-- values, and the replay refuses an unknown kind and a target that is not an
-- ID by itself, without relying on this constraint.
CREATE TABLE maintenance_revocations (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT    NOT NULL CHECK (kind IN ('session', 'token', 'principal', 'temp-admin', 'grant', 'join-token', 'provider')),
    target     TEXT    NOT NULL,
    end_reason TEXT    NOT NULL,
    actor      TEXT    NOT NULL,
    object     TEXT    NOT NULL,
    at_ms      INTEGER NOT NULL
);
