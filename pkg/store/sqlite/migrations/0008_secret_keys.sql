-- Secret data keys: one wrapped key per scope, keyed by namespace and stack
-- ID (ADR 23). A rename keeps the stack ID, so it moves no key row. The final
-- delete of a stack deletes its key in the same transaction: DeleteStack
-- removes the key row with the stack row.
CREATE TABLE secret_keys (
    namespace     TEXT NOT NULL REFERENCES namespaces (name),
    stack_id      TEXT NOT NULL,
    wrapper_id    TEXT NOT NULL,
    ciphertext    BLOB NOT NULL,
    created_at_ms INTEGER NOT NULL,
    PRIMARY KEY (namespace, stack_id)
) STRICT;
