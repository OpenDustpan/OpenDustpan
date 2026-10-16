-- minimum-binary-version: v0.1.0
-- Generated file. Do not edit it by hand. make regenerate-baseline writes it
-- from the sqlite migrations of origin/main (9e9c08978d02).
-- This baseline replaced every migration before it, as a one-time exception
-- to the rule that an applied migration never changes. No binary opens a
-- database or restores a backup from before it.

CREATE TABLE namespaces (
    name             TEXT    NOT NULL PRIMARY KEY,
    home_region      TEXT    NOT NULL,
    failover_version INTEGER NOT NULL,
    created_at_ms    INTEGER NOT NULL
, allow_forced_writes INTEGER NOT NULL DEFAULT 0, allow_unlocked_writes INTEGER NOT NULL DEFAULT 0, ip_allowlist TEXT NOT NULL DEFAULT '') STRICT;

CREATE TABLE stacks (
    namespace     TEXT    NOT NULL REFERENCES namespaces (name),
    project       TEXT    NOT NULL,
    name          TEXT    NOT NULL,
    version       INTEGER NOT NULL,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL, id TEXT NOT NULL DEFAULT '', deleted_at_ms INTEGER, deleted_name TEXT,
    PRIMARY KEY (namespace, project, name)
) STRICT;

CREATE TABLE "updates" (
    stack_id            TEXT    NOT NULL REFERENCES stacks (id) ON DELETE CASCADE,
    id                  TEXT    NOT NULL,
    kind                TEXT    NOT NULL,
    dry_run             INTEGER NOT NULL,
    status              TEXT    NOT NULL,
    version             INTEGER NOT NULL,
    revision            INTEGER NOT NULL,
    lease_digest        BLOB    NOT NULL,
    lease_expires_at_ms INTEGER NOT NULL,
    created_at_ms       INTEGER NOT NULL,
    started_at_ms       INTEGER NOT NULL,
    ended_at_ms         INTEGER NOT NULL, created_by TEXT NOT NULL DEFAULT '', restored_from INTEGER NOT NULL DEFAULT 0, journal_version INTEGER NOT NULL DEFAULT 0, journal_closed INTEGER NOT NULL DEFAULT 0, token_id TEXT NOT NULL DEFAULT '', journal_records_changed INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (stack_id, id),
    FOREIGN KEY (stack_id) REFERENCES stacks (id) ON DELETE CASCADE
) STRICT;

CREATE TABLE checkpoints (
    stack_id       TEXT    NOT NULL REFERENCES stacks (id) ON DELETE CASCADE,
    update_id      TEXT    NOT NULL,
    lease_digest   BLOB    NOT NULL,
    schema_version INTEGER NOT NULL,
    sequence       INTEGER NOT NULL,
    digest         BLOB    NOT NULL,
    invalid        INTEGER NOT NULL,
    features_json  TEXT    NOT NULL,
    deployment     BLOB    NOT NULL,
    saved_at_ms    INTEGER NOT NULL, blob_key TEXT, size INTEGER, blob_size INTEGER, blob_md5 BLOB,
    PRIMARY KEY (stack_id, update_id),
    FOREIGN KEY (stack_id) REFERENCES stacks (id) ON DELETE CASCADE,
    FOREIGN KEY (stack_id, update_id) REFERENCES updates (stack_id, id) ON DELETE CASCADE
) STRICT;

CREATE TABLE update_events (
    stack_id     TEXT    NOT NULL,
    update_id    TEXT    NOT NULL,
    sequence     INTEGER NOT NULL,
    timestamp_ms INTEGER NOT NULL,
    payload      BLOB    NOT NULL,
    PRIMARY KEY (stack_id, update_id, sequence),
    FOREIGN KEY (stack_id, update_id) REFERENCES updates (stack_id, id) ON DELETE CASCADE
) STRICT;

CREATE TABLE stack_tags (
    stack_id TEXT NOT NULL REFERENCES stacks (id) ON DELETE CASCADE,
    name     TEXT NOT NULL,
    value    TEXT NOT NULL,
    PRIMARY KEY (stack_id, name)
) STRICT;

CREATE TABLE secret_keys (
    namespace     TEXT NOT NULL REFERENCES namespaces (name),
    stack_id      TEXT NOT NULL,
    wrapper_id    TEXT NOT NULL,
    ciphertext    BLOB NOT NULL,
    created_at_ms INTEGER NOT NULL,
    PRIMARY KEY (namespace, stack_id)
) STRICT;

CREATE TABLE inventory (
    namespace       TEXT NOT NULL REFERENCES namespaces (name),
    tool            TEXT NOT NULL,
    name            TEXT NOT NULL,
    unit_id         TEXT NOT NULL,
    display_name    TEXT NOT NULL,
    last_changed_ms INTEGER NOT NULL,
    status          TEXT NOT NULL DEFAULT '',
    resource_count  INTEGER, lock_holder TEXT, lock_since_ms INTEGER, lock_operation TEXT,
    PRIMARY KEY (namespace, tool, name)
) STRICT;

CREATE TABLE workspaces (
    id                       TEXT    NOT NULL PRIMARY KEY,
    namespace                TEXT    NOT NULL REFERENCES namespaces (name),
    name                     TEXT    NOT NULL,
    revision                 INTEGER NOT NULL,
    lock_id                  TEXT    NOT NULL DEFAULT '',
    lock_holder              TEXT    NOT NULL DEFAULT '',
    lock_reason              TEXT    NOT NULL DEFAULT '',
    locked_at_ms             INTEGER NOT NULL DEFAULT 0,
    current_state_version_id TEXT    NOT NULL DEFAULT '',
    created_at_ms            INTEGER NOT NULL,
    updated_at_ms            INTEGER NOT NULL, allow_forced_writes INTEGER NOT NULL DEFAULT 0, allow_unlocked_writes INTEGER NOT NULL DEFAULT 0, deleted_at_ms INTEGER, deleted_name TEXT,
    UNIQUE (namespace, name)
) STRICT;

CREATE TABLE state_versions (
    id                    TEXT    NOT NULL PRIMARY KEY,
    workspace_id          TEXT    NOT NULL REFERENCES workspaces (id),
    status                TEXT    NOT NULL,
    serial                INTEGER NOT NULL,
    lineage               TEXT    NOT NULL,
    md5                   BLOB    NOT NULL,
    size                  INTEGER NOT NULL,
    content_key           TEXT    NOT NULL DEFAULT '',
    outputs               BLOB,
    force                 INTEGER NOT NULL DEFAULT 0,
    upload_digest         BLOB    NOT NULL,
    upload_expires_at_ms  INTEGER NOT NULL,
    holder                TEXT    NOT NULL DEFAULT '',
    lock_id               TEXT    NOT NULL DEFAULT '',
    created_at_ms         INTEGER NOT NULL,
    finalized_at_ms       INTEGER NOT NULL DEFAULT 0
, restored_from TEXT NOT NULL DEFAULT '', sealed INTEGER NOT NULL DEFAULT 0, sensitive_marks BLOB, fenced_by TEXT NOT NULL DEFAULT '', unlocked_write INTEGER NOT NULL DEFAULT 0) STRICT;

CREATE TABLE backups (
    id            TEXT    NOT NULL PRIMARY KEY,
    state         TEXT    NOT NULL,
    started_at_ms INTEGER NOT NULL,
    ended_at_ms   INTEGER NOT NULL DEFAULT 0,
    target        TEXT    NOT NULL DEFAULT '',
    error         TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE installation (
    id    INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
    state TEXT    NOT NULL DEFAULT 'normal' CHECK (state IN ('normal', 'maintenance'))
) STRICT;

CREATE TABLE restores (
    id            TEXT    NOT NULL PRIMARY KEY,
    state         TEXT    NOT NULL,
    backup_id     TEXT    NOT NULL,
    pre_backup_id TEXT    NOT NULL DEFAULT '',
    requested_by  TEXT    NOT NULL DEFAULT '',
    started_at_ms INTEGER NOT NULL,
    ended_at_ms   INTEGER NOT NULL DEFAULT 0,
    error         TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE update_journal_sequences (
    stack_id  TEXT    NOT NULL,
    update_id TEXT    NOT NULL,
    sequence  INTEGER NOT NULL,
    arrival   INTEGER NOT NULL,
    sha256    BLOB    NOT NULL,
    PRIMARY KEY (stack_id, update_id, sequence),
    FOREIGN KEY (stack_id, update_id, arrival) REFERENCES update_journal (stack_id, update_id, arrival) ON DELETE CASCADE
) STRICT;

CREATE TABLE "update_journal" (
    stack_id        TEXT    NOT NULL,
    update_id       TEXT    NOT NULL,
    arrival         INTEGER NOT NULL,
    blob_key        TEXT    NOT NULL,
    sha256          BLOB    NOT NULL,
    md5             BLOB    NOT NULL,
    size            INTEGER NOT NULL,
    lowest_sequence INTEGER NOT NULL,
    sequence_ids    TEXT    NOT NULL,
    operation_ids   TEXT    NOT NULL,
    PRIMARY KEY (stack_id, update_id, arrival),
    FOREIGN KEY (stack_id, update_id) REFERENCES updates (stack_id, id) ON DELETE CASCADE
) STRICT;

CREATE TABLE principals (
    id            TEXT    NOT NULL PRIMARY KEY,
    subject       TEXT    NOT NULL,
    display_name  TEXT    NOT NULL DEFAULT '',
    kind          TEXT    NOT NULL,
    source        TEXT    NOT NULL DEFAULT 'local',
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    deleted_at_ms INTEGER
, disabled_at_ms INTEGER, last_active_at_ms INTEGER NOT NULL DEFAULT 0, last_sign_in_at_ms INTEGER, namespace TEXT NOT NULL DEFAULT '', break_glass INTEGER NOT NULL DEFAULT 0) STRICT;

CREATE TABLE principal_groups (
    id            TEXT    NOT NULL PRIMARY KEY,
    name          TEXT    NOT NULL,
    name_key      TEXT    NOT NULL,
    description   TEXT    NOT NULL DEFAULT '',
    source        TEXT    NOT NULL DEFAULT 'local',
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    deleted_at_ms INTEGER
) STRICT;

CREATE TABLE group_members (
    group_id      TEXT    NOT NULL REFERENCES principal_groups (id),
    principal_id  TEXT    NOT NULL REFERENCES principals (id),
    added_at_ms   INTEGER NOT NULL,
    removed_at_ms INTEGER, source TEXT NOT NULL DEFAULT 'local', source_id TEXT NOT NULL DEFAULT '', state TEXT NOT NULL DEFAULT 'active', suspended_at_ms INTEGER NOT NULL DEFAULT 0, pinned INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (group_id, principal_id, added_at_ms)
) STRICT;

CREATE TABLE "audit_entries" (
    namespace    TEXT    NOT NULL,
    sequence     INTEGER NOT NULL,
    subject      TEXT    NOT NULL,
    idp_subject  TEXT    NOT NULL DEFAULT '',
    token_id     TEXT    NOT NULL DEFAULT '',
    action       TEXT    NOT NULL,
    object       TEXT    NOT NULL,
    version      TEXT    NOT NULL DEFAULT '',
    value_path   TEXT    NOT NULL DEFAULT '',
    at_ms        INTEGER NOT NULL,
    source_ip    TEXT    NOT NULL DEFAULT '',
    user_agent   TEXT    NOT NULL DEFAULT '',
    channel      TEXT    NOT NULL,
    kind         TEXT    NOT NULL,
    outcome      TEXT    NOT NULL,
    request_id   TEXT    NOT NULL DEFAULT '',
    prev_hash    TEXT    NOT NULL DEFAULT '',
    hash         TEXT    NOT NULL,
    value_count  INTEGER, changes TEXT, object_id TEXT NOT NULL DEFAULT '', fence_version INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (namespace, sequence)
) STRICT;

CREATE TABLE inventory_deleted (
    namespace      TEXT    NOT NULL REFERENCES namespaces (name),
    tool           TEXT    NOT NULL,
    unit_id        TEXT    NOT NULL,
    name           TEXT    NOT NULL,
    deleted_at_ms  INTEGER NOT NULL,
    purge_after_ms INTEGER NOT NULL,
    PRIMARY KEY (tool, unit_id)
) STRICT;

CREATE TABLE access_tokens (
    id              TEXT    NOT NULL PRIMARY KEY,
    principal_id    TEXT    NOT NULL REFERENCES principals (id),
    name            TEXT    NOT NULL,
    kind            TEXT    NOT NULL,
    digest          TEXT    NOT NULL,
    created_at_ms   INTEGER NOT NULL,
    expires_at_ms   INTEGER NOT NULL,
    last_used_at_ms INTEGER,
    revoked_at_ms   INTEGER,
    replaced_by     TEXT,
    created_by      TEXT    NOT NULL DEFAULT ''
, disabled_at_ms INTEGER) STRICT;

CREATE TABLE roles (
    id            TEXT    NOT NULL PRIMARY KEY,
    name          TEXT    NOT NULL,
    name_key      TEXT    NOT NULL,
    description   TEXT    NOT NULL DEFAULT '',
    actions       TEXT    NOT NULL,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    deleted_at_ms INTEGER
) STRICT;

CREATE TABLE grants (
    id            TEXT    NOT NULL PRIMARY KEY,
    subject       TEXT    NOT NULL,
    subject_kind  TEXT    NOT NULL,
    subject_id    TEXT    NOT NULL,
    role          TEXT    NOT NULL,
    object        TEXT    NOT NULL,
    target        TEXT    NOT NULL,
    object_kind   TEXT    NOT NULL,
    namespace     TEXT    NOT NULL DEFAULT '',
    condition     TEXT,
    created_at_ms INTEGER NOT NULL,
    created_by    TEXT    NOT NULL DEFAULT '',
    revoked_at_ms INTEGER,
    revoked_by    TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE audit_sink_cursors (
    sink          TEXT    NOT NULL,
    namespace     TEXT    NOT NULL,
    sequence      INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    PRIMARY KEY (sink, namespace)
) STRICT;

CREATE TABLE access_token_policy (
    id                  INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
    max_lifetime_ms     INTEGER NOT NULL,
    inactivity_ms       INTEGER NOT NULL,
    disable_tokens      INTEGER NOT NULL,
    disable_accounts    INTEGER NOT NULL,
    disable_break_glass INTEGER NOT NULL,
    updated_at_ms       INTEGER NOT NULL
) STRICT;

CREATE TABLE blob_purges (
    tool          TEXT    NOT NULL,
    unit_id       TEXT    NOT NULL,
    namespace     TEXT    NOT NULL,
    purged_at_ms  INTEGER NOT NULL,
    PRIMARY KEY (tool, unit_id)
) STRICT;

CREATE TABLE collections (
    id            TEXT    NOT NULL UNIQUE,
    namespace     TEXT    NOT NULL REFERENCES namespaces (name),
    name_key      TEXT    NOT NULL,
    name          TEXT    NOT NULL,
    created_at_ms INTEGER NOT NULL,
    PRIMARY KEY (namespace, name_key)
) STRICT;

CREATE TABLE collection_members (
    unit_id        TEXT    NOT NULL PRIMARY KEY,
    namespace      TEXT    NOT NULL,
    collection_key TEXT    NOT NULL,
    assigned_at_ms INTEGER NOT NULL,
    FOREIGN KEY (namespace, collection_key) REFERENCES collections (namespace, name_key)
) STRICT;

CREATE TABLE sessions (
    id              TEXT    NOT NULL PRIMARY KEY,
    principal_id    TEXT    NOT NULL REFERENCES principals (id),
    digest          TEXT    NOT NULL,
    provider        TEXT    NOT NULL,
    created_at_ms   INTEGER NOT NULL,
    last_seen_at_ms INTEGER NOT NULL,
    auth_time_ms    INTEGER NOT NULL,
    ended_at_ms     INTEGER,
    end_reason      TEXT    NOT NULL DEFAULT '',
    source_ip       TEXT    NOT NULL DEFAULT '',
    user_agent      TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE sign_in_requests (
    digest        TEXT    NOT NULL PRIMARY KEY,
    provider      TEXT    NOT NULL,
    nonce         TEXT    NOT NULL,
    verifier      TEXT    NOT NULL,
    return_path   TEXT    NOT NULL,
    created_at_ms INTEGER NOT NULL,
    expires_at_ms INTEGER NOT NULL
, max_age_ms INTEGER) STRICT;

CREATE TABLE session_policy (
    id            INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
    idle_ms       INTEGER NOT NULL,
    max_length_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
, token_freshness_ms INTEGER) STRICT;

CREATE TABLE audit_chain_state (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    state           TEXT    NOT NULL CHECK (state IN ('on', 'off')),
    changed_at_ms   INTEGER NOT NULL,
    changed_by      TEXT    NOT NULL,
    pending         INTEGER NOT NULL DEFAULT 0 CHECK (pending IN (0, 1)),
    -- entered_maintenance notes whether the chain entered maintenance mode itself.
    entered_maintenance INTEGER NOT NULL DEFAULT 0 CHECK (entered_maintenance IN (0, 1)),
    log_replaced    INTEGER NOT NULL DEFAULT 0 CHECK (log_replaced IN (0, 1)),
    migrating_at_ms INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE TABLE audit_sink_leases (
    sink          TEXT    NOT NULL PRIMARY KEY,
    holder        TEXT    NOT NULL,
    expires_at_ms INTEGER NOT NULL
) STRICT;

CREATE TABLE update_journal_refusals (
    stack_id     TEXT    NOT NULL,
    update_id    TEXT    NOT NULL,
    arrival      INTEGER NOT NULL,
    sequence_ids TEXT    NOT NULL,
    status       INTEGER NOT NULL,
    reason       TEXT    NOT NULL,
    PRIMARY KEY (stack_id, update_id, arrival),
    FOREIGN KEY (stack_id, update_id) REFERENCES updates (stack_id, id) ON DELETE CASCADE
) STRICT;

CREATE TABLE oidc_providers (
    provider_id   TEXT    NOT NULL PRIMARY KEY,
    issuer        TEXT    NOT NULL,
    created_at_ms INTEGER NOT NULL
) STRICT;

CREATE TABLE installation_secret_keys (
    scope_id      TEXT    NOT NULL PRIMARY KEY,
    wrapper_id    TEXT    NOT NULL,
    ciphertext    BLOB    NOT NULL,
    created_at_ms INTEGER NOT NULL
) STRICT;

CREATE TABLE sign_in_providers (
    provider_id   TEXT    NOT NULL PRIMARY KEY,
    issuer        TEXT    NOT NULL,
    display_name  TEXT    NOT NULL,
    client_id     TEXT    NOT NULL,
    client_secret BLOB    NOT NULL,
    scopes        TEXT    NOT NULL,
    auto_create   INTEGER NOT NULL,
    enabled       INTEGER NOT NULL,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    version       INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX stacks_id ON stacks (id);

CREATE UNIQUE INDEX updates_one_running ON updates (stack_id)
WHERE status = 'running' AND dry_run = 0 AND kind <> 'preview';

CREATE INDEX updates_history ON updates (stack_id, version)
WHERE status <> 'not started' AND dry_run = 0 AND kind <> 'preview';

CREATE INDEX inventory_unit_id ON inventory (unit_id);

CREATE INDEX state_versions_workspace_id ON state_versions (workspace_id);

CREATE INDEX state_versions_workspace_finalized ON state_versions (workspace_id, finalized_at_ms DESC);

CREATE UNIQUE INDEX principals_subject ON principals (subject) WHERE deleted_at_ms IS NULL;

CREATE UNIQUE INDEX principal_groups_name_key ON principal_groups (name_key) WHERE deleted_at_ms IS NULL;

CREATE UNIQUE INDEX group_members_current ON group_members (group_id, principal_id) WHERE removed_at_ms IS NULL;

CREATE INDEX group_members_principal ON group_members (principal_id) WHERE removed_at_ms IS NULL;

CREATE INDEX audit_entries_ns_time ON audit_entries (namespace, at_ms DESC, sequence DESC);

CREATE INDEX inventory_deleted_namespace ON inventory_deleted (namespace, tool, name);

CREATE UNIQUE INDEX access_tokens_digest ON access_tokens (digest);

CREATE INDEX access_tokens_created ON access_tokens (created_at_ms DESC, id DESC);

CREATE INDEX access_tokens_principal ON access_tokens (principal_id, created_at_ms DESC, id DESC);

CREATE UNIQUE INDEX roles_name_key ON roles (name_key) WHERE deleted_at_ms IS NULL;

CREATE UNIQUE INDEX grants_current ON grants (subject, role, target) WHERE revoked_at_ms IS NULL;

CREATE INDEX grants_target ON grants (target, subject, role) WHERE revoked_at_ms IS NULL;

CREATE INDEX grants_role ON grants (role) WHERE revoked_at_ms IS NULL;

CREATE INDEX grants_holder ON grants (subject_kind, subject_id) WHERE revoked_at_ms IS NULL;

CREATE INDEX inventory_deleted_purge_after ON inventory_deleted (purge_after_ms, tool, unit_id);

CREATE INDEX collection_members_collection ON collection_members (namespace, collection_key);

CREATE UNIQUE INDEX sessions_digest ON sessions (digest);

CREATE INDEX sessions_principal ON sessions (principal_id, created_at_ms DESC, id DESC);

CREATE INDEX sign_in_requests_expiry ON sign_in_requests (expires_at_ms);

CREATE INDEX blob_purges_age ON blob_purges (purged_at_ms, tool, unit_id);

CREATE INDEX principals_namespace ON principals (namespace, subject) WHERE deleted_at_ms IS NULL;

INSERT INTO installation (id, state) VALUES (1, 'normal');

INSERT INTO access_token_policy (id, max_lifetime_ms, inactivity_ms, disable_tokens, disable_accounts, disable_break_glass, updated_at_ms) VALUES (1, 31536000000, 7776000000, 1, 1, 0, 0);

INSERT INTO session_policy (id, idle_ms, max_length_ms, updated_at_ms, token_freshness_ms) VALUES (1, 900000, 43200000, 0, NULL);

INSERT INTO audit_chain_state (id, state, changed_at_ms, changed_by, pending, entered_maintenance, log_replaced, migrating_at_ms) VALUES (1, 'on', 0, '', 0, 0, 0, 0);
