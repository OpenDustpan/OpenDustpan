-- The retire of a runner joins the writes that the journal of the stay in
-- maintenance mode holds: the kind runner. The restore retires the runner
-- again after the swap. SQLite cannot change a CHECK constraint, so the table
-- is rebuilt with the same columns and rows. The CHECK stays a
-- defense-in-depth layer: the replay refuses an unknown kind by itself.
CREATE TABLE maintenance_revocations_new (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT    NOT NULL CHECK (kind IN ('session', 'token', 'principal', 'temp-admin', 'grant', 'join-token', 'provider', 'runner')),
    target     TEXT    NOT NULL,
    end_reason TEXT    NOT NULL,
    actor      TEXT    NOT NULL,
    object     TEXT    NOT NULL,
    at_ms      INTEGER NOT NULL
);

INSERT INTO maintenance_revocations_new (id, kind, target, end_reason, actor, object, at_ms)
SELECT id, kind, target, end_reason, actor, object, at_ms FROM maintenance_revocations;

DROP TABLE maintenance_revocations;

ALTER TABLE maintenance_revocations_new RENAME TO maintenance_revocations;
