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
    id         text   NOT NULL PRIMARY KEY,
    scope      text   NOT NULL,
    kind       text   NOT NULL,
    name       text   NOT NULL,
    platform   text   NOT NULL,
    target     text   NOT NULL,
    created_by text   NOT NULL,
    version    bigint NOT NULL
);

CREATE TABLE notification_sink_secrets (
    scope      text   NOT NULL,
    ref        text   NOT NULL,
    sink_id    text   NOT NULL,
    position   bigint NOT NULL,
    ciphertext bytea  NOT NULL,
    PRIMARY KEY (scope, ref)
);

CREATE TABLE notification_sink_refs (
    scope text NOT NULL,
    ref   text NOT NULL,
    PRIMARY KEY (scope, ref)
);

CREATE INDEX notification_sinks_scope ON notification_sinks (scope, id);

CREATE INDEX notification_sink_secrets_sink ON notification_sink_secrets (scope, sink_id, position);

-- The outbox. A notification commits with the change that it reports. The
-- sequence counts the notifications of one scope and the dedupe key names one
-- fact, so a second notification with the same scope and key is not stored.
-- links, attributes and recipients are JSON text.
CREATE TABLE notifications (
    scope      text   NOT NULL,
    sequence   bigint NOT NULL,
    id         text   NOT NULL UNIQUE,
    type       text   NOT NULL,
    object     text   NOT NULL,
    object_id  text   NOT NULL,
    subject    text   NOT NULL,
    agent      text   NOT NULL,
    severity   bigint NOT NULL,
    summary    text   NOT NULL,
    links      text   NOT NULL,
    attributes text   NOT NULL,
    recipients text   NOT NULL,
    dedupe_key text   NOT NULL,
    at_ms      bigint NOT NULL,
    PRIMARY KEY (scope, sequence),
    UNIQUE (scope, dedupe_key)
);

-- The last sequence of each scope. It never goes back, also when a retention
-- job deletes old notifications. An append locks the row of its scope until
-- the commit, so a later sequence never becomes visible before an earlier one.
CREATE TABLE notification_sequences (
    scope         text   NOT NULL PRIMARY KEY,
    last_sequence bigint NOT NULL
);

-- The cursor of each dispatcher in each scope.
CREATE TABLE notification_cursors (
    name     text   NOT NULL,
    scope    text   NOT NULL,
    sequence bigint NOT NULL,
    PRIMARY KEY (name, scope)
);

CREATE TABLE notification_subscriptions (
    id           text   NOT NULL PRIMARY KEY,
    scope        text   NOT NULL,
    sink_id      text   NOT NULL,
    types        text   NOT NULL,
    objects      text   NOT NULL,
    min_severity bigint NOT NULL,
    state        text   NOT NULL,
    reason       text   NOT NULL,
    created_by   text   NOT NULL,
    version      bigint NOT NULL
);

CREATE INDEX notification_subscriptions_scope ON notification_subscriptions (scope, id);

CREATE INDEX notification_subscriptions_sink ON notification_subscriptions (scope, sink_id);

-- A delivery of one notification to one subscription or one contact. owner and
-- lease_until_ms are the claim. The unique key makes a second enqueue of the
-- same route a no-op.
CREATE TABLE notification_deliveries (
    id              text   NOT NULL PRIMARY KEY,
    scope           text   NOT NULL,
    notification_id text   NOT NULL,
    subscription_id text   NOT NULL,
    sink_id         text   NOT NULL,
    contact         text   NOT NULL,
    principal       text   NOT NULL,
    state           text   NOT NULL,
    attempts        bigint NOT NULL,
    next_at_ms      bigint NOT NULL,
    last_error      text   NOT NULL,
    delivered_at_ms bigint NOT NULL,
    owner           text   NOT NULL,
    lease_until_ms  bigint NOT NULL,
    version         bigint NOT NULL,
    UNIQUE (scope, notification_id, subscription_id, contact)
);

CREATE INDEX notification_deliveries_due ON notification_deliveries (scope, next_at_ms, id) WHERE state = 'pending';

CREATE INDEX notification_deliveries_subscription ON notification_deliveries (scope, subscription_id, next_at_ms DESC, id DESC);
