-- The notification tables. A row belongs to a scope: the name of a namespace,
-- or @server for the installation. The scope is the name and not a foreign
-- key, as in audit_entries, because the server scope has no namespace row.
-- The store checks the fence of the namespace in the transaction of each
-- write.

-- The sink tables held the ID of a namespace. They hold the scope now, so that
-- a sink can belong to the server scope. No tag shipped the old shape, so the
-- tables are dropped and made again, and no row is copied.
DROP TABLE notification_sink_refs;

DROP TABLE notification_sink_secrets;

DROP TABLE notification_sinks;

CREATE TABLE notification_sinks (
    id         TEXT    NOT NULL PRIMARY KEY,
    scope      TEXT    NOT NULL,
    kind       TEXT    NOT NULL,
    name       TEXT    NOT NULL,
    platform   TEXT    NOT NULL,
    target     TEXT    NOT NULL,
    created_by TEXT    NOT NULL,
    version    INTEGER NOT NULL
) STRICT;

CREATE TABLE notification_sink_secrets (
    scope      TEXT    NOT NULL,
    ref        TEXT    NOT NULL,
    sink_id    TEXT    NOT NULL,
    position   INTEGER NOT NULL,
    ciphertext BLOB    NOT NULL,
    PRIMARY KEY (scope, ref)
) STRICT;

CREATE TABLE notification_sink_refs (
    scope TEXT NOT NULL,
    ref   TEXT NOT NULL,
    PRIMARY KEY (scope, ref)
) STRICT;

CREATE INDEX notification_sinks_scope ON notification_sinks (scope, id);

CREATE INDEX notification_sink_secrets_sink ON notification_sink_secrets (scope, sink_id, position);

-- The outbox. A notification commits with the change that it reports. The
-- sequence counts the notifications of one scope and the dedupe key names one
-- fact, so a second notification with the same scope and key is not stored.
-- links, attributes and recipients are JSON text.
CREATE TABLE notifications (
    scope      TEXT    NOT NULL,
    sequence   INTEGER NOT NULL,
    id         TEXT    NOT NULL UNIQUE,
    type       TEXT    NOT NULL,
    object     TEXT    NOT NULL,
    object_id  TEXT    NOT NULL,
    subject    TEXT    NOT NULL,
    agent      TEXT    NOT NULL,
    severity   INTEGER NOT NULL,
    summary    TEXT    NOT NULL,
    links      TEXT    NOT NULL,
    attributes TEXT    NOT NULL,
    recipients TEXT    NOT NULL,
    dedupe_key TEXT    NOT NULL,
    at_ms      INTEGER NOT NULL,
    PRIMARY KEY (scope, sequence),
    UNIQUE (scope, dedupe_key)
) STRICT;

-- The last sequence of each scope. It never goes back, also when a retention
-- job deletes old notifications.
CREATE TABLE notification_sequences (
    scope         TEXT    NOT NULL PRIMARY KEY,
    last_sequence INTEGER NOT NULL
) STRICT;

-- The cursor of each dispatcher in each scope.
CREATE TABLE notification_cursors (
    name     TEXT    NOT NULL,
    scope    TEXT    NOT NULL,
    sequence INTEGER NOT NULL,
    PRIMARY KEY (name, scope)
) STRICT;

CREATE TABLE notification_subscriptions (
    id           TEXT    NOT NULL PRIMARY KEY,
    scope        TEXT    NOT NULL,
    sink_id      TEXT    NOT NULL,
    types        TEXT    NOT NULL,
    objects      TEXT    NOT NULL,
    min_severity INTEGER NOT NULL,
    state        TEXT    NOT NULL,
    reason       TEXT    NOT NULL,
    created_by   TEXT    NOT NULL,
    version      INTEGER NOT NULL
) STRICT;

CREATE INDEX notification_subscriptions_scope ON notification_subscriptions (scope, id);

CREATE INDEX notification_subscriptions_sink ON notification_subscriptions (scope, sink_id);

-- A delivery of one notification to one subscription or one contact. owner and
-- lease_until_ms are the claim. The unique key makes a second enqueue of the
-- same route a no-op.
CREATE TABLE notification_deliveries (
    id              TEXT    NOT NULL PRIMARY KEY,
    scope           TEXT    NOT NULL,
    notification_id TEXT    NOT NULL,
    subscription_id TEXT    NOT NULL,
    sink_id         TEXT    NOT NULL,
    contact         TEXT    NOT NULL,
    principal       TEXT    NOT NULL,
    state           TEXT    NOT NULL,
    attempts        INTEGER NOT NULL,
    next_at_ms      INTEGER NOT NULL,
    last_error      TEXT    NOT NULL,
    delivered_at_ms INTEGER NOT NULL,
    owner           TEXT    NOT NULL,
    lease_until_ms  INTEGER NOT NULL,
    version         INTEGER NOT NULL,
    UNIQUE (scope, notification_id, subscription_id, contact)
) STRICT;

CREATE INDEX notification_deliveries_due ON notification_deliveries (scope, next_at_ms, id) WHERE state = 'pending';

CREATE INDEX notification_deliveries_subscription ON notification_deliveries (scope, subscription_id, next_at_ms DESC, id DESC);
