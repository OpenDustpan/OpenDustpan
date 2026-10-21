-- Notification sinks belong to a namespace and live in a backup. A sink is a
-- destination: an email address, the host of a chat webhook, or the URL of a
-- signed webhook. platform names the chat platform of a chat sink and is empty
-- for any other kind. A sink keeps its kind and its platform for its whole
-- life: a change of either is a new sink. version counts the writes of the
-- sink and guards a replace.
CREATE TABLE notification_sinks (
    id           text   NOT NULL PRIMARY KEY,
    namespace_id text   NOT NULL REFERENCES namespaces (id),
    kind         text   NOT NULL,
    name         text   NOT NULL,
    platform     text   NOT NULL,
    target       text   NOT NULL,
    created_by   text   NOT NULL,
    version      bigint NOT NULL
);

CREATE INDEX notification_sinks_namespace ON notification_sinks (namespace_id, id);

-- The secret items of a sink: the signing keys of a webhook, or the URL of a
-- chat webhook. ciphertext is sealed for the namespace and the sink before it
-- reaches the store, so the table never holds a plaintext value. position is
-- the place of the ref in the secret refs of the sink. The sink_id is not a
-- foreign key: the code deletes the values of a sink with the sink.
CREATE TABLE notification_sink_secrets (
    namespace_id text    NOT NULL REFERENCES namespaces (id),
    ref          text    NOT NULL,
    sink_id      text    NOT NULL,
    position     bigint  NOT NULL,
    ciphertext   bytea   NOT NULL,
    PRIMARY KEY (namespace_id, ref)
);

CREATE INDEX notification_sink_secrets_sink ON notification_sink_secrets (namespace_id, sink_id, position);

-- Every ref that a namespace ever held for a secret item of a sink, also after
-- the delete of the value or of the sink. A ref names one value for good: the
-- store refuses a new value under a ref that this table holds, so a sink whose
-- refs did not change sends to the place that a person approved.
CREATE TABLE notification_sink_refs (
    namespace_id text NOT NULL REFERENCES namespaces (id),
    ref          text NOT NULL,
    PRIMARY KEY (namespace_id, ref)
);
