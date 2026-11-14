-- minimum-binary-version: v0.1.0
-- The baseline of the sqlite store. It replaced every migration
-- before it. No binary opens a database or restores a backup from before it.
-- Until the first tag, a change to the schema edits this file in place and
-- adds no numbered migration. The server refuses a database that another
-- version of this file made. make regenerate-baseline writes it again from
-- the numbered migrations of a Git ref.

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER NOT NULL PRIMARY KEY,
    name    TEXT    NOT NULL
) STRICT;

ALTER TABLE schema_migrations ADD COLUMN min_binary_version TEXT NOT NULL DEFAULT '';

ALTER TABLE schema_migrations ADD COLUMN sha256 TEXT NOT NULL DEFAULT '';

CREATE TABLE namespaces (
    name             TEXT    NOT NULL PRIMARY KEY,
    primary_installation      TEXT    NOT NULL,
    failover_version INTEGER NOT NULL,
    created_at_ms    INTEGER NOT NULL
, allow_forced_writes INTEGER NOT NULL DEFAULT 0, allow_unlocked_writes INTEGER NOT NULL DEFAULT 0, ip_allowlist TEXT NOT NULL DEFAULT '', id TEXT NOT NULL, output_sharing_default TEXT NOT NULL DEFAULT 'nobody'
    CHECK (output_sharing_default IN ('nobody', 'namespace')), source_filter TEXT NOT NULL DEFAULT ''
    CHECK (length(CAST(source_filter AS BLOB)) <= 1048576 AND (source_filter = '' OR json_valid(source_filter)))) STRICT;

CREATE TABLE stacks (
    namespace     TEXT    NOT NULL REFERENCES namespaces (name),
    project       TEXT    NOT NULL,
    name          TEXT    NOT NULL,
    version       INTEGER NOT NULL,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL, id TEXT NOT NULL, deleted_at_ms INTEGER, deleted_name TEXT,
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
    ended_at_ms         INTEGER NOT NULL, created_by TEXT NOT NULL DEFAULT '', restored_from INTEGER NOT NULL DEFAULT 0, journal_version INTEGER NOT NULL DEFAULT 0, journal_closed INTEGER NOT NULL DEFAULT 0, token_id TEXT NOT NULL DEFAULT '', journal_records_changed INTEGER NOT NULL DEFAULT 0, job_id TEXT NOT NULL DEFAULT '', git_head TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (stack_id, id),
    FOREIGN KEY (stack_id) REFERENCES stacks (id) ON DELETE CASCADE
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
    resource_count  INTEGER, lock_holder TEXT, lock_since_ms INTEGER, lock_operation TEXT, last_release_lock_id TEXT NOT NULL DEFAULT '', last_release_holder TEXT NOT NULL DEFAULT '', last_release_by TEXT NOT NULL DEFAULT '', last_release_kind TEXT NOT NULL DEFAULT ''
    CHECK (last_release_kind IN ('', 'normal', 'forced')), last_release_at_ms INTEGER NOT NULL DEFAULT 0,
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
    updated_at_ms            INTEGER NOT NULL, allow_forced_writes INTEGER NOT NULL DEFAULT 0, allow_unlocked_writes INTEGER NOT NULL DEFAULT 0, deleted_at_ms INTEGER, deleted_name TEXT, last_release_lock_id TEXT NOT NULL DEFAULT '', last_release_holder TEXT NOT NULL DEFAULT '', last_release_by TEXT NOT NULL DEFAULT '', last_release_kind TEXT NOT NULL DEFAULT ''
    CHECK (last_release_kind IN ('', 'normal', 'forced')), last_release_at_ms INTEGER NOT NULL DEFAULT 0,
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
, restored_from TEXT NOT NULL DEFAULT '', sealed INTEGER NOT NULL DEFAULT 0, sensitive_marks BLOB, fenced_by TEXT NOT NULL DEFAULT '', unlocked_write INTEGER NOT NULL DEFAULT 0, origin_job TEXT NOT NULL DEFAULT '') STRICT;

CREATE TABLE backups (
    id            TEXT    NOT NULL PRIMARY KEY,
    state         TEXT    NOT NULL,
    started_at_ms INTEGER NOT NULL,
    ended_at_ms   INTEGER,
    target        TEXT    NOT NULL DEFAULT '',
    error         TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE installation (
    id    INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
    state TEXT    NOT NULL DEFAULT 'normal' CHECK (state IN ('normal', 'maintenance'))
, kind TEXT CHECK (kind IS NULL OR kind IN ('dev', 'production'))) STRICT;

CREATE TABLE restores (
    id            TEXT    NOT NULL PRIMARY KEY,
    state         TEXT    NOT NULL,
    backup_id     TEXT    NOT NULL,
    pre_backup_id TEXT    NOT NULL DEFAULT '',
    requested_by  TEXT    NOT NULL DEFAULT '',
    started_at_ms INTEGER NOT NULL,
    ended_at_ms   INTEGER,
    error         TEXT    NOT NULL DEFAULT ''
, dev_report TEXT NOT NULL DEFAULT '', maintenance_entered INTEGER NOT NULL DEFAULT 0
    CHECK (maintenance_entered IN (0, 1)), audit_events TEXT NOT NULL DEFAULT '[]') STRICT;

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
, closed_to_identity_provider INTEGER NOT NULL DEFAULT 0) STRICT;

CREATE TABLE group_members (
    group_id      TEXT    NOT NULL REFERENCES principal_groups (id),
    principal_id  TEXT    NOT NULL REFERENCES principals (id),
    added_at_ms   INTEGER NOT NULL,
    removed_at_ms INTEGER, source TEXT NOT NULL DEFAULT 'local', source_id TEXT NOT NULL DEFAULT '', state TEXT NOT NULL DEFAULT 'active', suspended_at_ms INTEGER NOT NULL DEFAULT 0, pinned INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (group_id, principal_id, added_at_ms)
) STRICT;

CREATE TABLE group_mappings (
    source        TEXT    NOT NULL,
    source_id     TEXT    NOT NULL,
    claim_value   TEXT    NOT NULL,
    group_id      TEXT    NOT NULL REFERENCES principal_groups (id),
    created_by    TEXT    NOT NULL,
    created_at_ms INTEGER NOT NULL,
    PRIMARY KEY (source, source_id, claim_value, group_id)
) STRICT;

CREATE TABLE member_removal_alerts (
    source_kind    TEXT    NOT NULL CHECK (source_kind IN ('identity-provider', 'scim-token')),
    source_id      TEXT    NOT NULL CHECK (source_id <> ''),
    alerted_at_ms  INTEGER NOT NULL CHECK (alerted_at_ms > 0),
    PRIMARY KEY (source_kind, source_id)
) STRICT;

CREATE TABLE member_removals (
    id             TEXT    NOT NULL PRIMARY KEY,
    source_kind    TEXT    NOT NULL CHECK (source_kind IN ('identity-provider', 'scim-token')),
    source_id      TEXT    NOT NULL CHECK (source_id <> ''),
    group_id       TEXT    NOT NULL CHECK (group_id <> ''),
    removed        INTEGER NOT NULL CHECK (removed >= 1),
    members_before INTEGER NOT NULL,
    at_ms          INTEGER NOT NULL CHECK (at_ms > 0),
    CHECK (members_before >= removed)
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
    value_count  INTEGER, changes TEXT, object_id TEXT NOT NULL DEFAULT '', fence_version INTEGER NOT NULL DEFAULT 0, reason TEXT NOT NULL DEFAULT '', principal_kind TEXT NOT NULL DEFAULT '', job_id TEXT NOT NULL DEFAULT '',
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
, exchange_lifetime_ms INTEGER NOT NULL DEFAULT 3600000) STRICT;

CREATE TABLE blob_purges (
    tool          TEXT    NOT NULL,
    unit_id       TEXT    NOT NULL,
    purged_at_ms  INTEGER NOT NULL, namespace_id TEXT NOT NULL,
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
, generation INTEGER NOT NULL DEFAULT 0 CHECK (generation >= 0)) STRICT;

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
, mode TEXT NOT NULL DEFAULT 'oidc' CHECK (mode IN ('oidc', 'oauth')), authorization_url TEXT NOT NULL DEFAULT '', token_url TEXT NOT NULL DEFAULT '', userinfo_url TEXT NOT NULL DEFAULT '', subject_claim TEXT NOT NULL DEFAULT '', name_claim TEXT NOT NULL DEFAULT '', username_claim TEXT NOT NULL DEFAULT '', email_claim TEXT NOT NULL DEFAULT '') STRICT;

CREATE TABLE durable_operations (
    id                  TEXT    NOT NULL PRIMARY KEY,
    start_key           TEXT    NOT NULL UNIQUE,
    kind                TEXT    NOT NULL,
    input               BLOB    NOT NULL,
    version             INTEGER NOT NULL,
    state               TEXT    NOT NULL
        CHECK (state IN ('pending', 'running', 'succeeded', 'failed', 'canceled')),
    attempt             INTEGER NOT NULL,
    expiries            INTEGER NOT NULL DEFAULT 0,
    failures            INTEGER NOT NULL DEFAULT 0,
    last_error          TEXT    NOT NULL,
    claim_owner         TEXT,
    claim_token         INTEGER NOT NULL,
    claim_expires_at_ms INTEGER,
    cancel_requested    INTEGER NOT NULL,
    not_before_ms       INTEGER NOT NULL,
    created_at_ms       INTEGER NOT NULL,
    updated_at_ms       INTEGER NOT NULL
) STRICT;

CREATE TABLE durable_steps (
    operation_id TEXT    NOT NULL REFERENCES durable_operations (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    seq          INTEGER NOT NULL,
    result       BLOB    NOT NULL,
    done_at_ms   INTEGER NOT NULL,
    PRIMARY KEY (operation_id, name),
    UNIQUE (operation_id, seq)
) STRICT;

CREATE TABLE durable_schedules (
    name               TEXT    NOT NULL PRIMARY KEY,
    interval_ms        INTEGER NOT NULL CHECK (interval_ms > 0),
    next_run_at_ms     INTEGER NOT NULL,
    last_claimed_at_ms INTEGER
) STRICT;

CREATE TABLE "checkpoints" (
    stack_id       TEXT    NOT NULL REFERENCES stacks (id) ON DELETE CASCADE,
    update_id      TEXT    NOT NULL,
    lease_digest   BLOB    NOT NULL,
    schema_version INTEGER NOT NULL,
    sequence       INTEGER NOT NULL,
    digest         BLOB    NOT NULL,
    invalid        INTEGER NOT NULL,
    features_json  TEXT    NOT NULL,
    saved_at_ms    INTEGER NOT NULL,
    blob_key       TEXT    NOT NULL CHECK (blob_key <> ''),
    size           INTEGER,
    blob_size      INTEGER,
    blob_md5       BLOB,
    PRIMARY KEY (stack_id, update_id),
    FOREIGN KEY (stack_id) REFERENCES stacks (id) ON DELETE CASCADE,
    FOREIGN KEY (stack_id, update_id) REFERENCES updates (stack_id, id) ON DELETE CASCADE
) STRICT;

CREATE TABLE sign_in_failures (
    source TEXT NOT NULL,
    window_end_ms INTEGER NOT NULL,
    count INTEGER NOT NULL,
    PRIMARY KEY (source, window_end_ms)
) STRICT;

CREATE TABLE object_context (
    namespace_id           TEXT    NOT NULL REFERENCES namespaces (id),
    object_kind            TEXT    NOT NULL,
    object_id              TEXT    NOT NULL,
    tool                   TEXT    NOT NULL,
    resource_key           TEXT    NOT NULL,
    source                 TEXT    NOT NULL,
    fields                 TEXT    NOT NULL,
    warning                TEXT    NOT NULL,
    item_id                TEXT    NOT NULL,
    item_seq               INTEGER NOT NULL,
    resource_deleted_at_ms INTEGER,
    marks_seq              INTEGER NOT NULL,
    updated_at_ms          INTEGER NOT NULL,
    updated_by             TEXT    NOT NULL,
    PRIMARY KEY (namespace_id, object_kind, object_id, resource_key, source),
    CHECK (object_kind IN ('namespace', 'collection', 'unit', 'resource', 'blueprint')),
    CHECK (source IN ('api', 'output'))
) STRICT;

CREATE TABLE object_context_owners (
    namespace_id TEXT NOT NULL,
    owner        TEXT NOT NULL,
    object_kind  TEXT NOT NULL,
    object_id    TEXT NOT NULL,
    resource_key TEXT NOT NULL,
    source       TEXT NOT NULL,
    PRIMARY KEY (namespace_id, owner, object_kind, object_id, resource_key, source),
    FOREIGN KEY (namespace_id, object_kind, object_id, resource_key, source)
        REFERENCES object_context (namespace_id, object_kind, object_id, resource_key, source)
        ON DELETE CASCADE
) STRICT;

CREATE TABLE inventory_change_heads (
    namespace_id TEXT    NOT NULL PRIMARY KEY,
    last_seq     INTEGER NOT NULL
) STRICT;

CREATE TABLE inventory_changes (
    namespace_id   TEXT    NOT NULL,
    seq            INTEGER NOT NULL,
    at_ms          INTEGER NOT NULL,
    tool           TEXT    NOT NULL,
    unit_id        TEXT    NOT NULL,
    name           TEXT    NOT NULL,
    display_name   TEXT    NOT NULL,
    status         TEXT    NOT NULL,
    collection     TEXT    NOT NULL,
    kind           TEXT    NOT NULL,
    resource_count INTEGER,
    PRIMARY KEY (namespace_id, seq)
) STRICT;

CREATE TABLE temp_admins (
  principal_id TEXT PRIMARY KEY,
  subject TEXT NOT NULL,
  expires_at_ms BIGINT NOT NULL,
  created_at_ms BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  reason TEXT NOT NULL,
  ended_at_ms BIGINT
, review_opened_at_ms BIGINT);

CREATE TABLE break_glass_reviews (
    id                      TEXT    NOT NULL PRIMARY KEY,
    use_kind                TEXT    NOT NULL,
    use_ref                 TEXT    NOT NULL,
    use_scope               TEXT    NOT NULL,
    use_object              TEXT    NOT NULL,
    use_subject             TEXT    NOT NULL,
    use_people              TEXT    NOT NULL,
    use_reason              TEXT    NOT NULL,
    use_at_ms               INTEGER NOT NULL,
    use_until_ms            INTEGER NOT NULL,
    state                   TEXT    NOT NULL,
    created_at_ms           INTEGER NOT NULL,
    due_at_ms               INTEGER NOT NULL,
    calendar                TEXT    NOT NULL,
    calendar_from_namespace TEXT    NOT NULL,
    calendar_from_group     TEXT    NOT NULL,
    step                    INTEGER NOT NULL,
    next_notice_at_ms       INTEGER NOT NULL,
    verdict_reviewer        TEXT    NOT NULL,
    verdict_note            TEXT    NOT NULL,
    verdict_auth            TEXT    NOT NULL,
    verdict_auth_at_ms      INTEGER NOT NULL,
    verdict_reviewed_at_ms  INTEGER NOT NULL,
    verdict_act_as          TEXT    NOT NULL,
    version                 INTEGER NOT NULL,
    CHECK (use_kind IN ('freeze-break', 'account-session', 'temp-admin')),
    CHECK (state IN ('pending', 'accepted', 'disputed'))
) STRICT;

CREATE TABLE gate_records (
    id            TEXT    NOT NULL PRIMARY KEY,
    namespace_id  TEXT    NOT NULL,
    point         TEXT    NOT NULL,
    change_kind   TEXT    NOT NULL,
    change_id     TEXT    NOT NULL,
    at_ms         INTEGER NOT NULL,
    last_at_ms    INTEGER NOT NULL,
    count         INTEGER NOT NULL,
    refusal_key   TEXT    NOT NULL,
    body          TEXT    NOT NULL
) STRICT;

CREATE TABLE imports (
    namespace_id TEXT    NOT NULL,
    id           TEXT    NOT NULL,
    kind         TEXT    NOT NULL,
    address      TEXT    NOT NULL,
    selection    TEXT    NOT NULL,
    credential   TEXT    NOT NULL,
    profile      TEXT    NOT NULL,
    profile_path TEXT    NOT NULL,
    options      TEXT    NOT NULL,
    state        TEXT    NOT NULL,
    reason       TEXT    NOT NULL,
    created_by   TEXT    NOT NULL,
    created_at_ms INTEGER NOT NULL,
    plan         TEXT    NOT NULL,
    ended_at_ms  INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, id),
    CHECK (state IN ('planning', 'planned', 'running', 'paused', 'holding', 'released', 'failed', 'canceled')),
    CHECK (credential IN ('profile', 'ephemeral', 'none'))
) STRICT;

CREATE TABLE imports_purged (
    namespace_id TEXT    NOT NULL,
    id           TEXT    NOT NULL,
    purged_at_ms INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, id)
) STRICT;

CREATE TABLE import_units (
    namespace_id   TEXT    NOT NULL,
    import_id      TEXT    NOT NULL,
    id             TEXT    NOT NULL,
    position       INTEGER NOT NULL,
    source_tool    TEXT    NOT NULL,
    source_name    TEXT    NOT NULL,
    target_name    TEXT    NOT NULL,
    target_tool    TEXT    NOT NULL,
    target_unit_id TEXT    NOT NULL,
    state          TEXT    NOT NULL,
    error_code     TEXT    NOT NULL,
    versions       INTEGER NOT NULL,
    committed      INTEGER NOT NULL,
    bytes          INTEGER NOT NULL,
    encryption     TEXT    NOT NULL,
    findings       TEXT    NOT NULL,
    PRIMARY KEY (namespace_id, import_id, id),
    FOREIGN KEY (namespace_id, import_id) REFERENCES imports (namespace_id, id),
    CHECK (state IN ('planned', 'skipped', 'running', 'paused', 'imported', 'failed', 'canceled'))
) STRICT;

CREATE TABLE import_ledger (
    namespace_id   TEXT    NOT NULL,
    import_id      TEXT    NOT NULL,
    import_unit_id TEXT    NOT NULL,
    version        TEXT    NOT NULL,
    source_sha256  TEXT    NOT NULL,
    stored_sha256  TEXT    NOT NULL,
    item_tool      TEXT    NOT NULL,
    item_unit_id   TEXT    NOT NULL,
    item_kind      TEXT    NOT NULL,
    item_id        TEXT    NOT NULL,
    committed_at_ms INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, import_id, import_unit_id, version)
) STRICT;

CREATE TABLE inventory_labels (
    namespace   TEXT NOT NULL REFERENCES namespaces (name),
    unit_id     TEXT NOT NULL,
    label_key   TEXT NOT NULL,
    label_value TEXT NOT NULL,
    PRIMARY KEY (unit_id, label_key)
) STRICT;

CREATE TABLE profiles (
    namespace_id  TEXT    NOT NULL,
    id            TEXT    NOT NULL,
    name          TEXT    NOT NULL,
    latest        INTEGER NOT NULL,
    created_by    TEXT    NOT NULL,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    deleted_at_ms INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, id),
    CHECK (latest >= 1)
) STRICT;

CREATE TABLE profile_revisions (
    namespace_id  TEXT    NOT NULL,
    profile_id    TEXT    NOT NULL,
    number        INTEGER NOT NULL,
    document      TEXT    NOT NULL,
    document_sha256 TEXT  NOT NULL,
    author        TEXT    NOT NULL,
    created_at_ms INTEGER NOT NULL,
    reason        TEXT    NOT NULL,
    PRIMARY KEY (namespace_id, profile_id, number),
    FOREIGN KEY (namespace_id, profile_id) REFERENCES profiles (namespace_id, id),
    CHECK (number >= 1)
) STRICT;

CREATE TABLE runner_pools (
    id             TEXT    NOT NULL PRIMARY KEY,
    namespace_id   TEXT    NOT NULL REFERENCES namespaces (id),
    name           TEXT    NOT NULL,
    purpose        TEXT    NOT NULL,
    lease_ms       INTEGER NOT NULL,
    concurrency    INTEGER NOT NULL,
    isolation      TEXT    NOT NULL,
    git_mode       TEXT    NOT NULL,
    git_helper     TEXT    NOT NULL,
    created_at_ms  INTEGER NOT NULL,
    updated_at_ms  INTEGER NOT NULL,
    UNIQUE (namespace_id, name),
    CHECK (purpose IN ('jobs', 'sweeps'))
) STRICT;

CREATE TABLE runner_join_tokens (
    id            TEXT    NOT NULL PRIMARY KEY,
    namespace_id  TEXT    NOT NULL REFERENCES namespaces (id),
    pool_id       TEXT    NOT NULL REFERENCES runner_pools (id),
    sha256        TEXT    NOT NULL UNIQUE,
    created_by    TEXT    NOT NULL,
    created_at_ms INTEGER NOT NULL,
    expires_at_ms INTEGER NOT NULL,
    used_at_ms    INTEGER NOT NULL,
    used_by       TEXT    NOT NULL,
    revoked_at_ms INTEGER NOT NULL
) STRICT;

CREATE TABLE runners (
    id                     TEXT    NOT NULL PRIMARY KEY,
    namespace_id           TEXT    NOT NULL REFERENCES namespaces (id),
    pool_id                TEXT    NOT NULL REFERENCES runner_pools (id),
    name                   TEXT    NOT NULL,
    kind                   TEXT    NOT NULL,
    isolation              TEXT    NOT NULL,
    labels                 TEXT    NOT NULL,
    release                TEXT    NOT NULL,
    key_public             BLOB    NOT NULL,
    key_created_at_ms      INTEGER NOT NULL,
    previous_key_public    BLOB    NOT NULL,
    previous_key_created_at_ms INTEGER NOT NULL,
    rotated_at_ms          INTEGER NOT NULL,
    joined_with            TEXT    NOT NULL,
    draining               INTEGER NOT NULL,
    retired_at_ms          INTEGER NOT NULL,
    last_seen_at_ms        INTEGER NOT NULL,
    joined_at_ms           INTEGER NOT NULL,
    CHECK (kind IN ('kubernetes', 'machine'))
) STRICT;

CREATE TABLE runner_claims (
    operation_id  TEXT    NOT NULL,
    token         INTEGER NOT NULL,
    claimed_at_ms INTEGER NOT NULL,
    job_id        TEXT    NOT NULL DEFAULT '',
    phase         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (operation_id, token)
) STRICT;

CREATE TABLE runner_handoff_tokens (
    sha256        TEXT    NOT NULL PRIMARY KEY,
    operation_id  TEXT    NOT NULL,
    token         INTEGER NOT NULL,
    runner_id     TEXT    NOT NULL,
    issued_at_ms  INTEGER NOT NULL,
    expires_at_ms INTEGER NOT NULL,
    used_at_ms    INTEGER NOT NULL,
    UNIQUE (operation_id, token)
) STRICT;

CREATE TABLE runner_sessions (
    id                TEXT    NOT NULL PRIMARY KEY,
    operation_id      TEXT    NOT NULL,
    token             INTEGER NOT NULL,
    runner_id         TEXT    NOT NULL,
    key_public        BLOB    NOT NULL,
    key_created_at_ms INTEGER NOT NULL,
    created_at_ms     INTEGER NOT NULL,
    UNIQUE (operation_id, token)
) STRICT;

CREATE TABLE runner_nonces (
    subject       TEXT    NOT NULL,
    nonce         TEXT    NOT NULL,
    expires_at_ms INTEGER NOT NULL,
    PRIMARY KEY (subject, nonce)
) STRICT;

CREATE TABLE workspace_blame_cursors (
    workspace_id TEXT    NOT NULL PRIMARY KEY REFERENCES workspaces (id),
    ordinal      INTEGER NOT NULL CHECK (ordinal >= 1)
) STRICT;

CREATE TABLE workspace_blame_items (
    workspace_id     TEXT    NOT NULL REFERENCES workspaces (id),
    state_version_id TEXT    NOT NULL,
    ordinal          INTEGER NOT NULL CHECK (ordinal >= 1),
    opaque           INTEGER NOT NULL CHECK (opaque IN (0, 1)),
    lineage_changed  INTEGER NOT NULL CHECK (lineage_changed IN (0, 1)),
    resource_count   INTEGER NOT NULL CHECK (resource_count >= 0),
    PRIMARY KEY (workspace_id, state_version_id),
    UNIQUE (workspace_id, ordinal)
) STRICT;

CREATE TABLE workspace_resource_changes (
    workspace_id     TEXT    NOT NULL,
    state_version_id TEXT    NOT NULL,
    address          TEXT    NOT NULL,
    ordinal          INTEGER NOT NULL,
    kind             TEXT    NOT NULL CHECK (kind IN ('created', 'updated', 'replaced', 'deleted')),
    at_ms            INTEGER NOT NULL,
    by_subject       TEXT    NOT NULL,
    by_token_id      TEXT    NOT NULL,
    by_job_id        TEXT    NOT NULL,
    changed_paths    INTEGER NOT NULL CHECK (changed_paths >= 0),
    restored         INTEGER NOT NULL CHECK (restored IN (0, 1)),
    forced           INTEGER NOT NULL CHECK (forced IN (0, 1)),
    lineage_changed  INTEGER NOT NULL CHECK (lineage_changed IN (0, 1)),
    PRIMARY KEY (workspace_id, state_version_id, address),
    FOREIGN KEY (workspace_id, state_version_id) REFERENCES workspace_blame_items (workspace_id, state_version_id)
) STRICT;

CREATE TABLE idempotency_records (
    principal     TEXT    NOT NULL,
    method        TEXT    NOT NULL,
    path          TEXT    NOT NULL,
    idem_key      TEXT    NOT NULL,
    fingerprint   BLOB    NOT NULL,
    token         TEXT    NOT NULL,
    status        INTEGER NOT NULL,
    headers       TEXT    NOT NULL,
    body          BLOB    NOT NULL,
    created_at_ms INTEGER NOT NULL,
    PRIMARY KEY (principal, method, path, idem_key),
    CHECK (status = 0 OR status BETWEEN 200 AND 499)
) STRICT;

CREATE TABLE idempotency_server_key (
    id          INTEGER NOT NULL PRIMARY KEY,
    wrapped_key BLOB    NOT NULL,
    wrapper_id  TEXT    NOT NULL,
    CHECK (id = 1)
) STRICT;

CREATE TABLE durable_heartbeats (
    owner TEXT    NOT NULL PRIMARY KEY,
    at_ms INTEGER NOT NULL
, lease_ms INTEGER NOT NULL DEFAULT 0) STRICT;

CREATE TABLE "manual_locks" (
    kind      TEXT    NOT NULL CHECK (kind IN ('unit', 'collection', 'namespace')),
    object_id TEXT    NOT NULL,
    namespace TEXT    NOT NULL REFERENCES namespaces (name),
    holder    TEXT    NOT NULL CHECK (holder <> ''),
    reason    TEXT    NOT NULL CHECK (reason <> '' AND length(reason) <= 500),
    since_ms  INTEGER NOT NULL,
    PRIMARY KEY (kind, object_id)
) STRICT;

CREATE TABLE audit_chain_check (
    id            INTEGER NOT NULL PRIMARY KEY,
    checked_at_ms INTEGER NOT NULL,
    invalid       INTEGER NOT NULL,
    CHECK (id = 1),
    CHECK (invalid >= 0)
) STRICT;

CREATE TABLE audit_chain_mark (
    scope        TEXT    NOT NULL PRIMARY KEY,
    epoch        INTEGER NOT NULL,
    sequence     INTEGER NOT NULL,
    marked_at_ms INTEGER NOT NULL,
    CHECK (scope <> ''),
    CHECK (epoch >= 0),
    CHECK (sequence >= 0)
) STRICT;

CREATE TABLE jobs (
    id                TEXT    NOT NULL PRIMARY KEY,
    namespace_id      TEXT    NOT NULL REFERENCES namespaces (id),
    unit_id           TEXT    NOT NULL,
    seq               INTEGER NOT NULL,
    state             TEXT    NOT NULL,
    wait              TEXT    NOT NULL,
    current_phase     INTEGER NOT NULL,
    current_changes   INTEGER NOT NULL CHECK (current_changes IN (0, 1)),
    current_operation TEXT    NOT NULL,
    idempotency_key   TEXT    NOT NULL,
    version           INTEGER NOT NULL,
    body              TEXT    NOT NULL,
    created_at_ms     INTEGER NOT NULL,
    updated_at_ms     INTEGER NOT NULL
, state_changed_at_ms INTEGER NOT NULL DEFAULT 0) STRICT;

CREATE TABLE job_freezes (
    id            TEXT    NOT NULL PRIMARY KEY,
    namespace_id  TEXT    NOT NULL REFERENCES namespaces (id),
    reason        TEXT    NOT NULL,
    starts_at_ms  INTEGER NOT NULL,
    ends_at_ms    INTEGER NOT NULL,
    created_by    TEXT    NOT NULL,
    ended_by      TEXT    NOT NULL
) STRICT;

CREATE TABLE unit_state (
    unit_id          TEXT    NOT NULL PRIMARY KEY,
    namespace_id     TEXT    NOT NULL REFERENCES namespaces (id),
    version_id       TEXT    NOT NULL DEFAULT '',
    final_version_id TEXT    NOT NULL DEFAULT '',
    final_commit     TEXT    NOT NULL DEFAULT '',
    final_job        TEXT    NOT NULL DEFAULT '',
    final_at_ms      INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE TABLE saved_views (
    id            TEXT    NOT NULL PRIMARY KEY,
    namespace_id  TEXT    NOT NULL REFERENCES namespaces (id),
    owner         TEXT    NOT NULL,
    name          TEXT    NOT NULL,
    query_json    TEXT    NOT NULL,
    created_at_ms INTEGER NOT NULL
) STRICT;

CREATE TABLE person_preferences (
    principal_id  TEXT    NOT NULL PRIMARY KEY REFERENCES principals (id),
    document      TEXT    NOT NULL CHECK (json_valid(document) AND json_type(document) = 'object' AND length(document) <= 4096),
    updated_at_ms INTEGER NOT NULL
) STRICT;

CREATE TABLE registry_releases (
    namespace_id     TEXT    NOT NULL REFERENCES namespaces (id),
    kind             TEXT    NOT NULL,
    name             TEXT    NOT NULL,
    system           TEXT    NOT NULL,
    version          TEXT    NOT NULL,
    sums_file        TEXT    NOT NULL,
    sums_sha256      TEXT    NOT NULL,
    signature_file   TEXT    NOT NULL,
    signature_sha256 TEXT    NOT NULL,
    key_fingerprint  TEXT    NOT NULL,
    protocols        TEXT    NOT NULL,
    published_by     TEXT    NOT NULL,
    archives_json    TEXT    NOT NULL,
    published_at_ms  INTEGER NOT NULL, stored_files_json TEXT NOT NULL DEFAULT ''
    CHECK (length(CAST(stored_files_json AS BLOB)) <= 1048576 AND (stored_files_json = '' OR json_valid(stored_files_json))),
    PRIMARY KEY (namespace_id, kind, name, system, version)
) STRICT;

CREATE TABLE registry_keys (
    namespace_id TEXT NOT NULL REFERENCES namespaces (id),
    fingerprint  TEXT NOT NULL,
    armored_key  TEXT NOT NULL,
    added_by     TEXT NOT NULL,
    added_at_ms  INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, fingerprint)
) STRICT;

CREATE TABLE git_host_app_registrations (
    kind                       TEXT    NOT NULL PRIMARY KEY,
    app_id                     TEXT    NOT NULL,
    base_url                   TEXT    NOT NULL,
    hook_id                    TEXT    NOT NULL UNIQUE,
    sealed_key                 BLOB    NOT NULL,
    key_set_id                 TEXT    NOT NULL,
    key_created_at_ms          INTEGER NOT NULL,
    sealed_event_secret        BLOB    NOT NULL,
    event_secret_set_id        TEXT    NOT NULL,
    event_secret_created_at_ms INTEGER NOT NULL,
    address_digest             TEXT    NOT NULL,
    version                    INTEGER NOT NULL CHECK (version >= 1),
    created_at_ms              INTEGER NOT NULL,
    updated_at_ms              INTEGER NOT NULL
) STRICT;

CREATE TABLE git_host_installations (
    installation_id TEXT    NOT NULL PRIMARY KEY,
    namespace_id    TEXT    NOT NULL REFERENCES namespaces (id),
    version         INTEGER NOT NULL CHECK (version >= 1),
    created_at_ms   INTEGER NOT NULL,
    updated_at_ms   INTEGER NOT NULL
) STRICT;

CREATE TABLE git_host_connections (
    namespace_id    TEXT    NOT NULL REFERENCES namespaces (id),
    id              TEXT    NOT NULL,
    kind            TEXT    NOT NULL,
    auth            TEXT    NOT NULL,
    app_scope       TEXT    NOT NULL,
    installation_id TEXT    NOT NULL,
    push            INTEGER NOT NULL CHECK (push IN (0, 1)),
    is_default      INTEGER NOT NULL CHECK (is_default IN (0, 1)),
    version         INTEGER NOT NULL CHECK (version >= 1),
    created_at_ms   INTEGER NOT NULL,
    updated_at_ms   INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, id)
) STRICT;

CREATE TABLE item_keys (
    id             TEXT    NOT NULL PRIMARY KEY,
    namespace_id   TEXT    NOT NULL REFERENCES namespaces (id),
    scope_id       TEXT    NOT NULL,
    kind           TEXT    NOT NULL CHECK (kind IN ('state', 'plan', 'job-log')),
    item           TEXT    NOT NULL,
    wrapper_id     TEXT    NOT NULL,
    created_at_ms  INTEGER NOT NULL,
    shredded_at_ms INTEGER NOT NULL CHECK (shredded_at_ms >= 0),
    blob_version   INTEGER NOT NULL CHECK (blob_version >= 1),
    blob_size      INTEGER NOT NULL CHECK (blob_size >= 1),
    blob_md5       BLOB NOT NULL CHECK (length(blob_md5) = 16)
) STRICT;

CREATE TABLE job_plans (
    id            TEXT    NOT NULL PRIMARY KEY,
    namespace_id  TEXT    NOT NULL REFERENCES namespaces (id),
    job_id        TEXT    NOT NULL,
    body          TEXT    NOT NULL,
    created_at_ms INTEGER NOT NULL
) STRICT;

CREATE TABLE job_logs (
    id            TEXT    NOT NULL PRIMARY KEY,
    namespace_id  TEXT    NOT NULL REFERENCES namespaces (id),
    job_id        TEXT    NOT NULL,
    phase         INTEGER NOT NULL CHECK (phase >= 0),
    step          TEXT    NOT NULL,
    token         INTEGER NOT NULL,
    created_at_ms INTEGER NOT NULL
) STRICT;

CREATE TABLE job_log_chunks (
    log_id       TEXT    NOT NULL,
    seq          INTEGER NOT NULL CHECK (seq >= 0),
    namespace_id TEXT    NOT NULL REFERENCES namespaces (id),
    sha256       TEXT    NOT NULL,
    size         INTEGER NOT NULL CHECK (size >= 0),
    confirmed    INTEGER NOT NULL CHECK (confirmed IN (0, 1)), stored_size INTEGER NOT NULL DEFAULT 0 CHECK (stored_size >= 0), stored_md5 BLOB NOT NULL DEFAULT X''
    CHECK (length(stored_md5) IN (0, 16)), blob_key TEXT NOT NULL DEFAULT '' CHECK (length(blob_key) <= 512),
    PRIMARY KEY (log_id, seq)
) STRICT;

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

CREATE TABLE triggers (
    namespace_id       TEXT    NOT NULL REFERENCES namespaces (id),
    id                 TEXT    NOT NULL,
    unit_tool          TEXT    NOT NULL,
    unit_id            TEXT    NOT NULL,
    kind               TEXT    NOT NULL CHECK (kind IN ('review-request-push', 'schedule', 'follows')),
    preset             TEXT    NOT NULL,
    cron               TEXT    NOT NULL,
    source_namespace   TEXT    NOT NULL,
    source_tool        TEXT    NOT NULL,
    source_unit_id     TEXT    NOT NULL,
    enabled            INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    last_saver_subject TEXT    NOT NULL,
    last_saver_id      TEXT    NOT NULL,
    created_at_ms      INTEGER NOT NULL,
    updated_at_ms      INTEGER NOT NULL,
    -- The next slot of a schedule trigger: the first run that a claim can still
    -- take, in Unix milliseconds. Put sets it from the schedule when it makes or
    -- switches on an enabled schedule trigger. The value 0 means that the row has
    -- no slot, which holds for a trigger of another kind and for a trigger that is
    -- off. An enabled schedule trigger with the value 0 is refused. A claim of a slot
    -- moves the value to the slot after it, in one transaction, so of many instances
    -- that claim one slot exactly one succeeds.
    next_slot_ms       INTEGER NOT NULL DEFAULT 0
        CHECK (next_slot_ms >= 0 AND (kind <> 'schedule' OR enabled = 0 OR next_slot_ms > 0)),
    PRIMARY KEY (namespace_id, id)
) STRICT;

CREATE TABLE "maintenance_revocations" (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT    NOT NULL CHECK (kind IN ('session', 'token', 'principal', 'temp-admin', 'grant', 'join-token', 'provider', 'runner')),
    target     TEXT    NOT NULL,
    end_reason TEXT    NOT NULL,
    actor      TEXT    NOT NULL,
    object     TEXT    NOT NULL,
    at_ms      INTEGER NOT NULL
);

CREATE TABLE instance_versions (
    id             TEXT    PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 64),
    binary_version TEXT    NOT NULL CHECK (length(binary_version) BETWEEN 1 AND 64),
    started_at_ms  INTEGER NOT NULL
) STRICT;

CREATE TABLE trust_rules (
    id              TEXT    NOT NULL PRIMARY KEY,
    namespace       TEXT    NOT NULL,
    name            TEXT    NOT NULL,
    issuer          TEXT    NOT NULL,
    audience        TEXT    NOT NULL,
    key_source      TEXT    NOT NULL CHECK (key_source IN ('discovery', 'static')),
    static_keys     TEXT    NOT NULL,
    conditions_json TEXT    NOT NULL,
    principal_id    TEXT    NOT NULL REFERENCES principals (id),
    created_by      TEXT    NOT NULL,
    created_at_ms   INTEGER NOT NULL
) STRICT;

CREATE TABLE self_service_blueprints (
    namespace_id     TEXT    NOT NULL REFERENCES namespaces (id),
    name             TEXT    NOT NULL,
    draft_version    INTEGER NOT NULL CHECK (draft_version >= 1),
    draft_json       TEXT    NOT NULL,
    draft_updated_at_ms INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, name)
) STRICT;

CREATE TABLE self_service_blueprint_versions (
    namespace_id    TEXT    NOT NULL,
    name            TEXT    NOT NULL,
    number          INTEGER NOT NULL CHECK (number >= 1),
    draft_version   INTEGER NOT NULL CHECK (draft_version >= 1),
    document_json   TEXT    NOT NULL,
    expr_version    TEXT    NOT NULL,
    published_by    TEXT    NOT NULL,
    published_at_ms INTEGER NOT NULL,
    retired         INTEGER NOT NULL CHECK (retired IN (0, 1)),
    PRIMARY KEY (namespace_id, name, number),
    FOREIGN KEY (namespace_id, name) REFERENCES self_service_blueprints (namespace_id, name)
) STRICT;

CREATE TABLE store_checks (
    id            TEXT    NOT NULL PRIMARY KEY CHECK (length(id) BETWEEN 5 AND 40),
    target        TEXT    NOT NULL CHECK (target IN ('blobs', 'backup')),
    state         TEXT    NOT NULL CHECK (state IN ('running', 'succeeded', 'failed')),
    requested_by  TEXT    NOT NULL,
    started_at_ms INTEGER NOT NULL,
    ended_at_ms   INTEGER,
    error         TEXT    NOT NULL DEFAULT '',
    report        TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE issuer_keys (
    namespace_id    TEXT    NOT NULL,
    id              TEXT    NOT NULL,
    state           TEXT    NOT NULL,
    public_key      BLOB    NOT NULL,
    sealed          BLOB    NOT NULL,
    created_at_ms   INTEGER NOT NULL,
    activated_at_ms INTEGER NOT NULL,
    retired_at_ms   INTEGER NOT NULL,
    deleted_at_ms   INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, id),
    CHECK (state IN ('next', 'active', 'retired', 'deleted')),
    CHECK ((state IN ('next', 'active')) = (length(sealed) > 0))
) STRICT;

CREATE TABLE issuer_settings (
    namespace_id         TEXT    NOT NULL,
    issuer_url           TEXT    NOT NULL,
    subject_template     TEXT    NOT NULL,
    allow_block_subjects INTEGER NOT NULL,
    include_actor_name   INTEGER NOT NULL,
    version              INTEGER NOT NULL,
    updated_at_ms        INTEGER NOT NULL,
    updated_by           TEXT    NOT NULL,
    copy_check           TEXT    NOT NULL,
    PRIMARY KEY (namespace_id),
    CHECK (allow_block_subjects IN (0, 1)),
    CHECK (include_actor_name IN (0, 1)),
    CHECK (version >= 0)
) STRICT;

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

CREATE TABLE notification_sequences (
    scope         TEXT    NOT NULL PRIMARY KEY,
    last_sequence INTEGER NOT NULL
) STRICT;

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

CREATE TABLE settings (
    scope_id      TEXT    NOT NULL,
    key           TEXT    NOT NULL CHECK (length(key) BETWEEN 1 AND 64),
    value         TEXT    NOT NULL CHECK (length(value) <= 1024),
    updated_at_ms INTEGER NOT NULL,
    PRIMARY KEY (scope_id, key)
) STRICT;

CREATE TABLE settings_state (
    scope_id      TEXT    NOT NULL PRIMARY KEY,
    revision      INTEGER NOT NULL CHECK (revision >= 0),
    static_digest TEXT    NOT NULL
) STRICT;

CREATE TABLE import_upload_commits (
    namespace_id    TEXT    NOT NULL,
    import_id       TEXT    NOT NULL,
    unit_dir        TEXT    NOT NULL,
    sequence        INTEGER NOT NULL,
    facts           TEXT    NOT NULL,
    committed_at_ms INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, import_id, unit_dir, sequence),
    CHECK (sequence >= 1)
) STRICT;

CREATE TABLE unit_settings (
    unit_id       TEXT    NOT NULL PRIMARY KEY,
    namespace_id  TEXT    NOT NULL REFERENCES namespaces (id),
    version       INTEGER NOT NULL CHECK (version >= 1),
    body          TEXT    NOT NULL,
    updated_at_ms INTEGER NOT NULL
) STRICT;

CREATE TABLE unit_defaults (
    namespace_id  TEXT    NOT NULL PRIMARY KEY REFERENCES namespaces (id),
    pool          TEXT    NOT NULL,
    version       INTEGER NOT NULL CHECK (version >= 1),
    updated_at_ms INTEGER NOT NULL
) STRICT;

CREATE TABLE placement_rules (
    id             TEXT    NOT NULL PRIMARY KEY,
    namespace      TEXT    NOT NULL REFERENCES namespaces (name),
    position       INTEGER NOT NULL CHECK (position >= 1),
    tool           TEXT    NOT NULL,
    project        TEXT    NOT NULL,
    stack          TEXT    NOT NULL,
    collection_key TEXT    NOT NULL,
    created_by     TEXT    NOT NULL,
    created_at_ms  INTEGER NOT NULL,
    FOREIGN KEY (namespace, collection_key) REFERENCES collections (namespace, name_key)
) STRICT;

CREATE TABLE local_users (
    id                 TEXT    NOT NULL PRIMARY KEY,
    principal_id       TEXT    NOT NULL UNIQUE REFERENCES principals (id),
    user_name          TEXT    NOT NULL UNIQUE CHECK (
        length(user_name) BETWEEN 1 AND 64
        AND user_name GLOB '[a-z0-9]*'
        AND user_name NOT GLOB '*[^a-z0-9._-]*'
    ),
    password_hash      TEXT    NOT NULL DEFAULT '' CHECK (
        password_hash = ''
        OR (length(password_hash) <= 256 AND substr(password_hash, 1, 15) = '$argon2id$v=19$')
    ),
    password_set_at_ms INTEGER NOT NULL DEFAULT 0,
    change_required    INTEGER NOT NULL DEFAULT 0 CHECK (change_required IN (0, 1)),
    demo               INTEGER NOT NULL DEFAULT 0 CHECK (demo IN (0, 1)),
    created_by         TEXT    NOT NULL,
    created_at_ms      INTEGER NOT NULL,
    UNIQUE (id, principal_id),
    CHECK (password_hash <> '' OR (change_required = 0 AND password_set_at_ms = 0))
) STRICT;

CREATE TABLE local_user_resets (
    id                   TEXT    NOT NULL PRIMARY KEY,
    user_id              TEXT    NOT NULL UNIQUE,
    principal_id         TEXT    NOT NULL,
    digest               TEXT    NOT NULL UNIQUE CHECK (length(digest) = 64 AND digest NOT GLOB '*[^0-9a-f]*'),
    purpose              TEXT    NOT NULL CHECK (purpose IN ('set-up', 'reset')),
    created_by           TEXT    NOT NULL,
    reason               TEXT    NOT NULL DEFAULT '',
    revoke_access_tokens INTEGER NOT NULL DEFAULT 0 CHECK (revoke_access_tokens IN (0, 1)),
    created_at_ms        INTEGER NOT NULL,
    expires_at_ms        INTEGER NOT NULL CHECK (expires_at_ms > created_at_ms),
    FOREIGN KEY (user_id, principal_id) REFERENCES local_users (id, principal_id) ON DELETE CASCADE
) STRICT;

CREATE TABLE passkey_challenges (
    digest        TEXT    NOT NULL PRIMARY KEY CHECK (length(digest) = 64 AND digest NOT GLOB '*[^0-9a-f]*'),
    kind          TEXT    NOT NULL CHECK (kind IN ('register', 'sign-in', 'second-factor', 'fresh')),
    principal_id  TEXT    NOT NULL DEFAULT '' CHECK (length(principal_id) <= 128),
    pending_id    TEXT    NOT NULL DEFAULT '' CHECK (length(pending_id) <= 128),
    session_id    TEXT    NOT NULL DEFAULT '' CHECK (length(session_id) <= 128),
    source        TEXT    NOT NULL DEFAULT '' CHECK (length(source) <= 64 AND source NOT GLOB '*[^ -~]*'),
    source_group  TEXT    NOT NULL DEFAULT '' CHECK (length(source_group) <= 64 AND source_group NOT GLOB '*[^ -~]*'),
    created_at_ms INTEGER NOT NULL,
    expires_at_ms INTEGER NOT NULL CHECK (expires_at_ms > created_at_ms AND expires_at_ms - created_at_ms <= 300000),
    CHECK ((kind = 'sign-in') = (principal_id = '')),
    CHECK ((kind = 'sign-in') = (source <> '')),
    CHECK ((kind = 'sign-in') = (source_group <> ''))
) STRICT;

CREATE TABLE passkeys (
    id              TEXT    NOT NULL PRIMARY KEY CHECK (substr(id, 1, 4) = 'pky-' AND length(id) <= 128),
    principal_id    TEXT    NOT NULL REFERENCES principals (id),
    name            TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    credential_id   BLOB    NOT NULL UNIQUE CHECK (length(credential_id) BETWEEN 1 AND 1023),
    public_key      BLOB    NOT NULL CHECK (length(public_key) BETWEEN 1 AND 4096),
    transports      TEXT    NOT NULL DEFAULT '' CHECK (length(transports) <= 64 AND transports NOT GLOB '*[^a-z,-]*'),
    aaguid          BLOB    NOT NULL CHECK (length(aaguid) = 16),
    sign_count      INTEGER NOT NULL DEFAULT 0 CHECK (sign_count BETWEEN 0 AND 4294967295),
    backup_eligible INTEGER NOT NULL DEFAULT 0 CHECK (backup_eligible IN (0, 1)),
    created_at_ms   INTEGER NOT NULL,
    last_used_at_ms INTEGER NOT NULL DEFAULT 0 CHECK (last_used_at_ms >= 0),
    suspended_at_ms INTEGER NOT NULL DEFAULT 0 CHECK (suspended_at_ms >= 0)
) STRICT;

CREATE TABLE principal_session_generations (
    principal_id TEXT    NOT NULL PRIMARY KEY REFERENCES principals (id),
    generation   INTEGER NOT NULL CHECK (generation >= 2)
) STRICT;

CREATE TABLE name_locks (
    scope            TEXT    NOT NULL CHECK (length(scope) BETWEEN 1 AND 32 AND scope NOT GLOB '*[^a-z]*'),
    name             TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 256),
    sub              TEXT    NOT NULL DEFAULT '' CHECK (length(sub) <= 256),
    failures         INTEGER NOT NULL DEFAULT 0 CHECK (failures >= 0),
    locked_until_ms  INTEGER NOT NULL DEFAULT 0 CHECK (locked_until_ms >= 0),
    level            INTEGER NOT NULL DEFAULT 0 CHECK (level BETWEEN 0 AND 64),
    last_failure_ms  INTEGER NOT NULL DEFAULT 0 CHECK (last_failure_ms >= 0),
    last_lock_end_ms INTEGER NOT NULL DEFAULT 0 CHECK (last_lock_end_ms >= 0),
    reset_after_ms   INTEGER NOT NULL DEFAULT 0 CHECK (reset_after_ms >= 0),
    PRIMARY KEY (scope, name, sub)
) STRICT;

CREATE TABLE name_lock_key (
    id  INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
    key BLOB    NOT NULL CHECK (length(key) = 32)
) STRICT;

-- Records of the CAS tickets that a server used. A ticket gives one sign-in
-- only, at every server of the installation and after a restart. The table
-- keeps the SHA-256 of a ticket in lower-case hex, never the ticket. A record
-- ends at until_ms and refuses nothing at or after it.
CREATE TABLE used_tickets (
    provider_id   TEXT    NOT NULL CHECK (length(provider_id) BETWEEN 1 AND 256),
    ticket_digest TEXT    NOT NULL CHECK (length(ticket_digest) = 64 AND ticket_digest NOT GLOB '*[^0-9a-f]*'),
    until_ms      INTEGER NOT NULL CHECK (until_ms > 0),
    PRIMARY KEY (provider_id, ticket_digest)
) STRICT;

CREATE INDEX used_tickets_until ON used_tickets (until_ms);

-- Links from an account of a Git host to a principal, and the one-time requests
-- that make them. A link and a request belong to the installation, not to a
-- namespace: a person has one principal for every namespace. An account is
-- named by the kind of the Git host, the host name of its API and the stable
-- account ID that the Git host gives. The primary key compares the three
-- columns byte for byte, so IDs that differ only in case are two accounts. The
-- login is text for a person to read and is never compared. A link goes away
-- with its principal, in the same transaction as the end of the principal.
CREATE TABLE forge_account_links (
    kind         TEXT NOT NULL CHECK (kind IN ('github', 'gitlab')),
    host         TEXT NOT NULL,
    account_id   TEXT NOT NULL,
    login        TEXT NOT NULL,
    principal_id TEXT NOT NULL REFERENCES principals (id),
    linked_at_ms INTEGER NOT NULL,
    PRIMARY KEY (kind, host, account_id)
) STRICT;

-- A request holds the SHA-256 of its one-time token and never the token. The
-- principal is the person who started it. The proved_* columns hold the account
-- that a signed event of the Git host named, and proved_at_ms is 0 until then.
-- used_at_ms is 0 until the person accepts. A request is good for 5 minutes from
-- created_at_ms.
CREATE TABLE forge_link_requests (
    token_sha256       TEXT NOT NULL PRIMARY KEY CHECK (length(token_sha256) = 64),
    principal_id       TEXT NOT NULL REFERENCES principals (id),
    created_at_ms      INTEGER NOT NULL,
    expires_at_ms      INTEGER NOT NULL,
    proved_kind        TEXT NOT NULL,
    proved_host        TEXT NOT NULL,
    proved_account_id  TEXT NOT NULL,
    proved_login       TEXT NOT NULL,
    proved_delivery_id TEXT NOT NULL,
    proved_at_ms       INTEGER NOT NULL,
    used_at_ms         INTEGER NOT NULL,
    CHECK (expires_at_ms > created_at_ms AND proved_at_ms >= 0 AND used_at_ms >= 0)
) STRICT;

CREATE TABLE gate_objects (
    namespace_id  TEXT NOT NULL,
    scope_key     TEXT NOT NULL,
    kind          TEXT NOT NULL,
    id            TEXT NOT NULL,
    level         TEXT NOT NULL,
    unit_id       TEXT NOT NULL,
    version       INTEGER NOT NULL,
    body          TEXT NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    PRIMARY KEY (namespace_id, scope_key, kind, id),
    CHECK (kind IN ('gate', 'access-limit', 'approval-rule', 'ownership-rule', 'path-set',
                    'approval-default', 'carry-over')),
    CHECK (level IN ('installation', 'namespace', 'collection', 'unit')),
    CHECK ((level = 'unit') = (unit_id <> '')),
    CHECK (level <> 'unit' OR scope_key = 'unit/' || unit_id),
    CHECK (version >= 1)
) STRICT;

CREATE TABLE gate_references (
    ref_kind     TEXT NOT NULL,
    ref_id       TEXT NOT NULL,
    namespace_id TEXT NOT NULL,
    scope_key    TEXT NOT NULL,
    owner_kind   TEXT NOT NULL,
    owner_id     TEXT NOT NULL,
    PRIMARY KEY (ref_kind, ref_id, namespace_id, scope_key, owner_kind, owner_id),
    FOREIGN KEY (namespace_id, scope_key, owner_kind, owner_id)
        REFERENCES gate_objects (namespace_id, scope_key, kind, id) ON DELETE CASCADE,
    CHECK (ref_kind IN ('approval-rule', 'path-set'))
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

CREATE INDEX member_removals_source ON member_removals (source_kind, source_id, at_ms);

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

CREATE UNIQUE INDEX backups_one_running ON backups ((state = 'running'))
    WHERE state = 'running';

CREATE UNIQUE INDEX restores_one_running ON restores ((state IN ('checking', 'applying')))
    WHERE state IN ('checking', 'applying');

CREATE INDEX durable_operations_open ON durable_operations (created_at_ms, id)
    WHERE state IN ('pending', 'running');

CREATE INDEX durable_operations_created ON durable_operations (created_at_ms, id);

CREATE INDEX sign_in_failures_window_end ON sign_in_failures (window_end_ms);

CREATE UNIQUE INDEX namespaces_id ON namespaces (id);

CREATE INDEX object_context_owners_entry
    ON object_context_owners (namespace_id, object_kind, object_id, resource_key, source);

CREATE INDEX inventory_changes_unit ON inventory_changes (namespace_id, tool, unit_id, seq, at_ms);

CREATE UNIQUE INDEX break_glass_reviews_use ON break_glass_reviews (use_kind, use_ref);

CREATE INDEX break_glass_reviews_due ON break_glass_reviews (state, next_notice_at_ms);

CREATE INDEX gate_records_change ON gate_records (namespace_id, change_kind, change_id, at_ms, id);

CREATE INDEX gate_records_point ON gate_records (namespace_id, point, at_ms, id);

CREATE INDEX gate_records_refusal ON gate_records (namespace_id, refusal_key, last_at_ms) WHERE refusal_key <> '';

CREATE INDEX imports_created ON imports (namespace_id, created_at_ms, id);

CREATE INDEX import_ledger_order ON import_ledger (namespace_id, import_id, import_unit_id, committed_at_ms, version);

CREATE INDEX inventory_labels_namespace ON inventory_labels (namespace, unit_id);

CREATE UNIQUE INDEX profiles_live_name ON profiles (namespace_id, name) WHERE deleted_at_ms = 0;

CREATE INDEX runner_join_tokens_pool ON runner_join_tokens (pool_id, created_at_ms, id);

CREATE UNIQUE INDEX runners_name ON runners (pool_id, name) WHERE retired_at_ms = 0;

CREATE INDEX runner_nonces_expiry ON runner_nonces (expires_at_ms);

CREATE INDEX workspace_resource_changes_address ON workspace_resource_changes (workspace_id, address, ordinal DESC);

CREATE INDEX idempotency_records_created ON idempotency_records (created_at_ms);

CREATE INDEX durable_heartbeats_at ON durable_heartbeats (at_ms);

CREATE INDEX manual_locks_namespace ON manual_locks (namespace);

CREATE UNIQUE INDEX jobs_one_change ON jobs (unit_id)
    WHERE current_changes = 1 AND state IN ('dispatched', 'running', 'canceling');

CREATE UNIQUE INDEX jobs_key ON jobs (namespace_id, idempotency_key)
    WHERE idempotency_key <> '';

CREATE UNIQUE INDEX jobs_list ON jobs (namespace_id, seq);

CREATE INDEX jobs_unit ON jobs (unit_id, seq);

CREATE INDEX jobs_open ON jobs (namespace_id, state)
    WHERE state NOT IN ('succeeded', 'failed', 'canceled', 'rejected', 'expired');

CREATE INDEX job_freezes_namespace ON job_freezes (namespace_id, starts_at_ms);

CREATE UNIQUE INDEX saved_views_owner_name ON saved_views (namespace_id, owner, name);

CREATE INDEX saved_views_owner ON saved_views (owner);

CREATE INDEX git_host_installations_namespace ON git_host_installations (namespace_id);

CREATE UNIQUE INDEX git_host_connections_installation ON git_host_connections (namespace_id, kind, installation_id);

CREATE UNIQUE INDEX git_host_connections_default ON git_host_connections (namespace_id) WHERE is_default = 1;

CREATE UNIQUE INDEX item_keys_item ON item_keys (namespace_id, scope_id, kind, item);

CREATE INDEX item_keys_scope ON item_keys (namespace_id, scope_id, id);

CREATE INDEX item_keys_wrapper ON item_keys (wrapper_id) WHERE shredded_at_ms = 0;

CREATE INDEX job_plans_job ON job_plans (job_id);

CREATE INDEX job_logs_job ON job_logs (job_id);

CREATE UNIQUE INDEX drift_results_job ON drift_results (job_id);

CREATE INDEX drift_results_namespace ON drift_results (namespace_id);

-- The links of one principal, for the list of a person.
CREATE INDEX forge_account_links_principal ON forge_account_links (principal_id);

-- The open and the expired requests of one principal, for the limit and the clean-up.
CREATE INDEX forge_link_requests_principal ON forge_link_requests (principal_id, expires_at_ms);

CREATE INDEX inventory_namespace_unit_id ON inventory (namespace, unit_id);

CREATE INDEX inventory_labels_value ON inventory_labels (namespace, label_key, label_value, unit_id);

CREATE INDEX workspaces_live_name ON workspaces (namespace, name) WHERE deleted_at_ms IS NULL;

CREATE INDEX triggers_unit ON triggers (namespace_id, unit_tool, unit_id);

CREATE INDEX triggers_source ON triggers (namespace_id, source_tool, source_unit_id)
    WHERE kind = 'follows' AND enabled = 1;

CREATE UNIQUE INDEX trust_rules_name ON trust_rules (namespace, name);

CREATE INDEX trust_rules_principal ON trust_rules (principal_id);

CREATE UNIQUE INDEX self_service_blueprint_versions_draft ON self_service_blueprint_versions (namespace_id, name, draft_version);

CREATE UNIQUE INDEX store_checks_one_running ON store_checks ((state = 'running')) WHERE state = 'running';

CREATE INDEX store_checks_target_ended ON store_checks (target, ended_at_ms);

CREATE INDEX store_checks_started_at_ms ON store_checks (started_at_ms DESC);

CREATE UNIQUE INDEX issuer_keys_one_active ON issuer_keys (namespace_id) WHERE state = 'active';

CREATE UNIQUE INDEX issuer_keys_one_next ON issuer_keys (namespace_id) WHERE state = 'next';

CREATE INDEX notification_sinks_scope ON notification_sinks (scope, id);

CREATE INDEX notification_sink_secrets_sink ON notification_sink_secrets (scope, sink_id, position);

CREATE INDEX notification_subscriptions_scope ON notification_subscriptions (scope, id);

CREATE INDEX notification_subscriptions_sink ON notification_subscriptions (scope, sink_id);

CREATE INDEX notification_deliveries_due ON notification_deliveries (scope, next_at_ms, id) WHERE state = 'pending';

CREATE INDEX notification_deliveries_subscription ON notification_deliveries (scope, subscription_id, next_at_ms DESC, id DESC);

CREATE INDEX unit_settings_namespace ON unit_settings (namespace_id);

CREATE INDEX placement_rules_order ON placement_rules (namespace, position);

CREATE INDEX local_user_resets_expiry ON local_user_resets (expires_at_ms);

CREATE INDEX passkey_challenges_expiry ON passkey_challenges (expires_at_ms);
CREATE INDEX passkey_challenges_source ON passkey_challenges (source, expires_at_ms);
CREATE INDEX passkey_challenges_source_group ON passkey_challenges (source_group, expires_at_ms);

CREATE INDEX passkeys_principal ON passkeys (principal_id, created_at_ms, id);

CREATE INDEX gate_objects_kind_id ON gate_objects (kind, id);

CREATE INDEX gate_objects_namespace ON gate_objects (namespace_id, level);

CREATE INDEX gate_objects_unit ON gate_objects (namespace_id, unit_id);

CREATE INDEX gate_references_owner ON gate_references (namespace_id, scope_key, owner_kind, owner_id);

CREATE TRIGGER namespaces_id_insert BEFORE INSERT ON namespaces
WHEN NEW.id NOT GLOB 'ns-?*'
BEGIN
    SELECT RAISE(ABORT, 'namespace id must start with ns- and name the namespace');
END;

CREATE TRIGGER namespaces_id_update BEFORE UPDATE OF id ON namespaces
WHEN NEW.id NOT GLOB 'ns-?*'
BEGIN
    SELECT RAISE(ABORT, 'namespace id must start with ns- and name the namespace');
END;

INSERT INTO installation (id, state, kind) VALUES (1, 'normal', NULL);

INSERT INTO access_token_policy (id, max_lifetime_ms, inactivity_ms, disable_tokens, disable_accounts, disable_break_glass, updated_at_ms, exchange_lifetime_ms) VALUES (1, 31536000000, 7776000000, 1, 1, 0, 0, 3600000);

INSERT INTO session_policy (id, idle_ms, max_length_ms, updated_at_ms, token_freshness_ms) VALUES (1, 900000, 43200000, 0, NULL);

INSERT INTO audit_chain_state (id, state, changed_at_ms, changed_by, pending, entered_maintenance, log_replaced, migrating_at_ms) VALUES (1, 'on', 0, '', 0, 0, 0, 0);
