-- minimum-binary-version: v0.1.0
-- The baseline of the postgres store. It replaced every migration
-- before it. No binary opens a database or restores a backup from before it.
-- Until the first tag, a change to the schema edits this file in place and
-- adds no numbered migration. The server refuses a database that another
-- version of this file made. make regenerate-baseline writes it again from
-- the numbered migrations of a Git ref.
-- pg_dump major version: 18

CREATE TABLE IF NOT EXISTS schema_migrations (
    version BIGINT NOT NULL PRIMARY KEY,
    name    TEXT   NOT NULL
);

ALTER TABLE schema_migrations ADD COLUMN min_binary_version TEXT NOT NULL DEFAULT '';

ALTER TABLE schema_migrations ADD COLUMN sha256 TEXT NOT NULL DEFAULT '';

CREATE TABLE access_token_policy (
    id bigint NOT NULL,
    max_lifetime_ms bigint NOT NULL,
    inactivity_ms bigint NOT NULL,
    disable_tokens boolean NOT NULL,
    disable_accounts boolean NOT NULL,
    disable_break_glass boolean NOT NULL,
    updated_at_ms bigint NOT NULL,
    exchange_lifetime_ms bigint DEFAULT 3600000 NOT NULL,
    CONSTRAINT access_token_policy_id_check CHECK ((id = 1))
);

CREATE TABLE access_tokens (
    id text NOT NULL,
    principal_id text NOT NULL,
    name text NOT NULL,
    kind text NOT NULL,
    digest text NOT NULL,
    created_at_ms bigint NOT NULL,
    expires_at_ms bigint NOT NULL,
    last_used_at_ms bigint,
    revoked_at_ms bigint,
    replaced_by text,
    created_by text DEFAULT ''::text NOT NULL,
    disabled_at_ms bigint
);

CREATE TABLE audit_chain_check (
    id smallint NOT NULL,
    checked_at_ms bigint NOT NULL,
    invalid bigint NOT NULL,
    CONSTRAINT audit_chain_check_id_check CHECK ((id = 1)),
    CONSTRAINT audit_chain_check_invalid_check CHECK ((invalid >= 0))
);

CREATE TABLE audit_chain_mark (
    scope text NOT NULL,
    epoch bigint NOT NULL,
    sequence bigint NOT NULL,
    marked_at_ms bigint NOT NULL,
    CONSTRAINT audit_chain_mark_epoch_check CHECK ((epoch >= 0)),
    CONSTRAINT audit_chain_mark_scope_check CHECK ((scope <> ''::text)),
    CONSTRAINT audit_chain_mark_sequence_check CHECK ((sequence >= 0))
);

CREATE TABLE audit_chain_state (
    id smallint NOT NULL,
    state text NOT NULL,
    changed_at_ms bigint NOT NULL,
    changed_by text NOT NULL,
    pending boolean DEFAULT false NOT NULL,
    entered_maintenance boolean DEFAULT false NOT NULL,
    log_replaced boolean DEFAULT false NOT NULL,
    migrating_at_ms bigint DEFAULT 0 NOT NULL,
    CONSTRAINT audit_chain_state_id_check CHECK ((id = 1)),
    CONSTRAINT audit_chain_state_state_check CHECK ((state = ANY (ARRAY['on'::text, 'off'::text])))
);

CREATE TABLE audit_entries (
    namespace text NOT NULL,
    sequence bigint NOT NULL,
    subject text NOT NULL,
    idp_subject text DEFAULT ''::text NOT NULL,
    token_id text DEFAULT ''::text NOT NULL,
    action text NOT NULL,
    object text NOT NULL,
    version text DEFAULT ''::text NOT NULL,
    value_path text DEFAULT ''::text NOT NULL,
    at_ms bigint NOT NULL,
    source_ip text DEFAULT ''::text NOT NULL,
    user_agent text DEFAULT ''::text NOT NULL,
    channel text NOT NULL,
    kind text NOT NULL,
    outcome text NOT NULL,
    request_id text DEFAULT ''::text NOT NULL,
    prev_hash text DEFAULT ''::text NOT NULL,
    hash text NOT NULL,
    value_count bigint,
    changes text,
    object_id text DEFAULT ''::text NOT NULL,
    fence_version bigint DEFAULT 0 NOT NULL,
    reason text DEFAULT ''::text NOT NULL,
    principal_kind text DEFAULT ''::text NOT NULL,
    job_id text DEFAULT ''::text NOT NULL
);

CREATE TABLE audit_sink_cursors (
    sink text NOT NULL,
    namespace text NOT NULL,
    sequence bigint NOT NULL,
    updated_at_ms bigint NOT NULL
);

CREATE TABLE audit_sink_leases (
    sink text NOT NULL,
    holder text NOT NULL,
    expires_at_ms bigint NOT NULL
);

CREATE TABLE blob_purges (
    tool text NOT NULL,
    unit_id text NOT NULL,
    purged_at_ms bigint NOT NULL,
    namespace_id text NOT NULL
);

CREATE TABLE break_glass_reviews (
    id text NOT NULL,
    use_kind text NOT NULL,
    use_ref text NOT NULL,
    use_scope text NOT NULL,
    use_object text NOT NULL,
    use_subject text NOT NULL,
    use_people text NOT NULL,
    use_reason text NOT NULL,
    use_at_ms bigint NOT NULL,
    use_until_ms bigint NOT NULL,
    state text NOT NULL,
    created_at_ms bigint NOT NULL,
    due_at_ms bigint NOT NULL,
    calendar text NOT NULL,
    calendar_from_namespace text NOT NULL,
    calendar_from_group text NOT NULL,
    step bigint NOT NULL,
    next_notice_at_ms bigint NOT NULL,
    verdict_reviewer text NOT NULL,
    verdict_note text NOT NULL,
    verdict_auth text NOT NULL,
    verdict_auth_at_ms bigint NOT NULL,
    verdict_reviewed_at_ms bigint NOT NULL,
    verdict_act_as text NOT NULL,
    version bigint NOT NULL,
    CONSTRAINT break_glass_reviews_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'accepted'::text, 'disputed'::text]))),
    CONSTRAINT break_glass_reviews_use_kind_check CHECK ((use_kind = ANY (ARRAY['freeze-break'::text, 'account-session'::text, 'temp-admin'::text])))
);

CREATE TABLE checkpoints (
    update_id text NOT NULL,
    lease_digest bytea NOT NULL,
    schema_version bigint NOT NULL,
    sequence bigint NOT NULL,
    digest bytea NOT NULL,
    invalid boolean NOT NULL,
    features jsonb NOT NULL,
    saved_at_ms bigint NOT NULL,
    stack_id text NOT NULL,
    blob_key text NOT NULL,
    size bigint,
    blob_size bigint,
    blob_md5 bytea,
    CONSTRAINT checkpoints_blob_key_check CHECK ((blob_key <> ''::text))
);

CREATE TABLE collection_members (
    unit_id text NOT NULL,
    namespace text NOT NULL,
    collection_key text NOT NULL,
    assigned_at_ms bigint NOT NULL
);

CREATE TABLE collections (
    id text NOT NULL,
    namespace text NOT NULL,
    name_key text NOT NULL,
    name text NOT NULL,
    created_at_ms bigint NOT NULL
);

CREATE TABLE drift_results (
    unit_id text NOT NULL,
    namespace_id text NOT NULL,
    tool text NOT NULL,
    job_id text NOT NULL,
    status text NOT NULL,
    checked_at_ms bigint NOT NULL,
    truncated boolean NOT NULL,
    resources_json text NOT NULL,
    CONSTRAINT drift_results_status_check CHECK ((status = ANY (ARRAY['drifted'::text, 'in-sync'::text])))
);

CREATE TABLE durable_heartbeats (
    owner text NOT NULL,
    at_ms bigint NOT NULL,
    lease_ms bigint DEFAULT 0 NOT NULL
);

CREATE TABLE durable_operations (
    id text NOT NULL,
    start_key text NOT NULL,
    kind text NOT NULL,
    input bytea NOT NULL,
    version bigint NOT NULL,
    state text NOT NULL,
    attempt bigint NOT NULL,
    expiries bigint DEFAULT 0 NOT NULL,
    failures bigint DEFAULT 0 NOT NULL,
    last_error text NOT NULL,
    claim_owner text,
    claim_token bigint NOT NULL,
    claim_expires_at_ms bigint,
    cancel_requested boolean NOT NULL,
    not_before_ms bigint NOT NULL,
    created_at_ms bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    CONSTRAINT durable_operations_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'running'::text, 'succeeded'::text, 'failed'::text, 'canceled'::text])))
);

CREATE TABLE durable_schedules (
    name text NOT NULL,
    interval_ms bigint NOT NULL,
    next_run_at_ms bigint NOT NULL,
    last_claimed_at_ms bigint,
    CONSTRAINT durable_schedules_interval_ms_check CHECK ((interval_ms > 0))
);

CREATE TABLE durable_steps (
    operation_id text NOT NULL,
    name text NOT NULL,
    seq bigint NOT NULL,
    result bytea NOT NULL,
    done_at_ms bigint NOT NULL
);

CREATE TABLE forge_account_links (
    kind text NOT NULL,
    host text NOT NULL,
    account_id text NOT NULL,
    login text NOT NULL,
    principal_id text NOT NULL,
    linked_at_ms bigint NOT NULL,
    CONSTRAINT forge_account_links_kind_check CHECK ((kind = ANY (ARRAY['github'::text, 'gitlab'::text])))
);

CREATE TABLE forge_link_requests (
    token_sha256 text NOT NULL,
    principal_id text NOT NULL,
    created_at_ms bigint NOT NULL,
    expires_at_ms bigint NOT NULL,
    proved_kind text NOT NULL,
    proved_host text NOT NULL,
    proved_account_id text NOT NULL,
    proved_login text NOT NULL,
    proved_delivery_id text NOT NULL,
    proved_at_ms bigint NOT NULL,
    used_at_ms bigint NOT NULL,
    CONSTRAINT forge_link_requests_check CHECK (((expires_at_ms > created_at_ms) AND (proved_at_ms >= 0) AND (used_at_ms >= 0))),
    CONSTRAINT forge_link_requests_token_sha256_check CHECK ((length(token_sha256) = 64))
);

CREATE TABLE gate_objects (
    namespace_id text NOT NULL,
    scope_key text NOT NULL,
    kind text NOT NULL,
    id text NOT NULL,
    level text NOT NULL,
    unit_id text NOT NULL,
    version bigint NOT NULL,
    body text NOT NULL,
    updated_at_ms bigint NOT NULL,
    CONSTRAINT gate_objects_check CHECK (((level = 'unit'::text) = (unit_id <> ''::text))),
    CONSTRAINT gate_objects_check1 CHECK (((level <> 'unit'::text) OR (scope_key = ('unit/'::text || unit_id)))),
    CONSTRAINT gate_objects_kind_check CHECK ((kind = ANY (ARRAY['gate'::text, 'access-limit'::text, 'approval-rule'::text, 'ownership-rule'::text, 'path-set'::text, 'approval-default'::text, 'carry-over'::text]))),
    CONSTRAINT gate_objects_level_check CHECK ((level = ANY (ARRAY['installation'::text, 'namespace'::text, 'collection'::text, 'unit'::text]))),
    CONSTRAINT gate_objects_version_check CHECK ((version >= 1))
);

CREATE TABLE gate_records (
    id text NOT NULL,
    namespace_id text NOT NULL,
    point text NOT NULL,
    change_kind text NOT NULL,
    change_id text NOT NULL,
    at_ms bigint NOT NULL,
    last_at_ms bigint NOT NULL,
    count bigint NOT NULL,
    refusal_key text NOT NULL,
    body text NOT NULL
);

CREATE TABLE gate_references (
    ref_kind text NOT NULL,
    ref_id text NOT NULL,
    namespace_id text NOT NULL,
    scope_key text NOT NULL,
    owner_kind text NOT NULL,
    owner_id text NOT NULL,
    CONSTRAINT gate_references_ref_kind_check CHECK ((ref_kind = ANY (ARRAY['approval-rule'::text, 'path-set'::text])))
);

CREATE TABLE git_host_app_registrations (
    kind text NOT NULL,
    app_id text NOT NULL,
    base_url text NOT NULL,
    hook_id text NOT NULL,
    sealed_key bytea NOT NULL,
    key_set_id text NOT NULL,
    key_created_at_ms bigint NOT NULL,
    sealed_event_secret bytea NOT NULL,
    event_secret_set_id text NOT NULL,
    event_secret_created_at_ms bigint NOT NULL,
    address_digest text NOT NULL,
    version bigint NOT NULL,
    created_at_ms bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    CONSTRAINT git_host_app_registrations_version_check CHECK ((version >= 1))
);

CREATE TABLE git_host_connections (
    namespace_id text NOT NULL,
    id text NOT NULL,
    kind text NOT NULL,
    auth text NOT NULL,
    app_scope text NOT NULL,
    installation_id text NOT NULL,
    push boolean NOT NULL,
    is_default boolean NOT NULL,
    version bigint NOT NULL,
    created_at_ms bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    CONSTRAINT git_host_connections_version_check CHECK ((version >= 1))
);

CREATE TABLE git_host_installations (
    installation_id text NOT NULL,
    namespace_id text NOT NULL,
    version bigint NOT NULL,
    created_at_ms bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    CONSTRAINT git_host_installations_version_check CHECK ((version >= 1))
);

CREATE TABLE grants (
    id text NOT NULL,
    subject text NOT NULL COLLATE pg_catalog."C",
    subject_kind text NOT NULL,
    subject_id text NOT NULL,
    role text NOT NULL COLLATE pg_catalog."C",
    object text NOT NULL COLLATE pg_catalog."C",
    target text NOT NULL COLLATE pg_catalog."C",
    object_kind text NOT NULL,
    namespace text DEFAULT ''::text NOT NULL,
    condition text,
    created_at_ms bigint NOT NULL,
    created_by text DEFAULT ''::text NOT NULL,
    revoked_at_ms bigint,
    revoked_by text DEFAULT ''::text NOT NULL
);

CREATE TABLE group_mappings (
    source text NOT NULL,
    source_id text NOT NULL,
    claim_value text NOT NULL,
    group_id text NOT NULL,
    created_by text NOT NULL,
    created_at_ms bigint NOT NULL
);

CREATE TABLE group_members (
    group_id text NOT NULL,
    principal_id text NOT NULL,
    added_at_ms bigint NOT NULL,
    removed_at_ms bigint,
    source text DEFAULT 'local'::text NOT NULL,
    source_id text DEFAULT ''::text NOT NULL,
    state text DEFAULT 'active'::text NOT NULL,
    suspended_at_ms bigint DEFAULT 0 NOT NULL,
    pinned boolean DEFAULT false NOT NULL
);

CREATE TABLE idempotency_records (
    principal text NOT NULL,
    method text NOT NULL,
    path text NOT NULL,
    idem_key text NOT NULL,
    fingerprint bytea NOT NULL,
    token text NOT NULL,
    status bigint NOT NULL,
    headers text NOT NULL,
    body bytea NOT NULL,
    created_at_ms bigint NOT NULL,
    CONSTRAINT idempotency_records_status_check CHECK (((status = 0) OR ((status >= 200) AND (status <= 499))))
);

CREATE TABLE idempotency_server_key (
    id bigint NOT NULL,
    wrapped_key bytea NOT NULL,
    wrapper_id text NOT NULL,
    CONSTRAINT idempotency_server_key_id_check CHECK ((id = 1))
);

CREATE TABLE import_ledger (
    namespace_id text NOT NULL,
    import_id text NOT NULL,
    import_unit_id text NOT NULL,
    version text NOT NULL,
    source_sha256 text NOT NULL,
    stored_sha256 text NOT NULL,
    item_tool text NOT NULL,
    item_unit_id text NOT NULL,
    item_kind text NOT NULL,
    item_id text NOT NULL,
    committed_at_ms bigint NOT NULL
);

CREATE TABLE import_units (
    namespace_id text NOT NULL,
    import_id text NOT NULL,
    id text NOT NULL,
    "position" bigint NOT NULL,
    source_tool text NOT NULL,
    source_name text NOT NULL,
    target_name text NOT NULL,
    target_tool text NOT NULL,
    target_unit_id text NOT NULL,
    state text NOT NULL,
    error_code text NOT NULL,
    versions bigint NOT NULL,
    committed bigint NOT NULL,
    bytes bigint NOT NULL,
    encryption text NOT NULL,
    findings text NOT NULL,
    CONSTRAINT import_units_state_check CHECK ((state = ANY (ARRAY['planned'::text, 'skipped'::text, 'running'::text, 'paused'::text, 'imported'::text, 'failed'::text, 'canceled'::text])))
);

CREATE TABLE import_upload_commits (
    namespace_id text NOT NULL,
    import_id text NOT NULL,
    unit_dir text NOT NULL,
    sequence bigint NOT NULL,
    facts text NOT NULL,
    committed_at_ms bigint NOT NULL,
    CONSTRAINT import_upload_commits_sequence_check CHECK ((sequence >= 1))
);

CREATE TABLE imports (
    namespace_id text NOT NULL,
    id text NOT NULL,
    kind text NOT NULL,
    address text NOT NULL,
    selection text NOT NULL,
    credential text NOT NULL,
    profile text NOT NULL,
    profile_path text NOT NULL,
    options text NOT NULL,
    state text NOT NULL,
    reason text NOT NULL,
    created_by text NOT NULL,
    created_at_ms bigint NOT NULL,
    plan text NOT NULL,
    ended_at_ms bigint NOT NULL,
    CONSTRAINT imports_credential_check CHECK ((credential = ANY (ARRAY['profile'::text, 'ephemeral'::text, 'none'::text]))),
    CONSTRAINT imports_state_check CHECK ((state = ANY (ARRAY['planning'::text, 'planned'::text, 'running'::text, 'paused'::text, 'holding'::text, 'released'::text, 'failed'::text, 'canceled'::text])))
);

CREATE TABLE imports_purged (
    namespace_id text NOT NULL,
    id text NOT NULL,
    purged_at_ms bigint NOT NULL
);

CREATE TABLE installation_secret_keys (
    scope_id text NOT NULL,
    wrapper_id text NOT NULL,
    ciphertext bytea NOT NULL,
    created_at_ms bigint NOT NULL
);

CREATE TABLE inventory (
    namespace text NOT NULL,
    tool text NOT NULL,
    name text NOT NULL,
    unit_id text NOT NULL,
    display_name text NOT NULL,
    last_changed_ms bigint NOT NULL,
    status text DEFAULT ''::text NOT NULL,
    resource_count bigint,
    lock_holder text,
    lock_since_ms bigint,
    lock_operation text,
    last_release_lock_id text DEFAULT ''::text NOT NULL,
    last_release_holder text DEFAULT ''::text NOT NULL,
    last_release_by text DEFAULT ''::text NOT NULL,
    last_release_kind text DEFAULT ''::text NOT NULL,
    last_release_at_ms bigint DEFAULT 0 NOT NULL,
    CONSTRAINT inventory_last_release_kind_check CHECK ((last_release_kind = ANY (ARRAY[''::text, 'normal'::text, 'forced'::text])))
);

CREATE TABLE inventory_change_heads (
    namespace_id text NOT NULL,
    last_seq bigint NOT NULL
);

CREATE TABLE inventory_changes (
    namespace_id text NOT NULL,
    seq bigint NOT NULL,
    at_ms bigint NOT NULL,
    tool text NOT NULL,
    unit_id text NOT NULL,
    name text NOT NULL,
    display_name text NOT NULL,
    status text NOT NULL,
    collection text NOT NULL,
    kind text NOT NULL,
    resource_count bigint
);

CREATE TABLE inventory_deleted (
    namespace text NOT NULL,
    tool text NOT NULL,
    unit_id text NOT NULL,
    name text NOT NULL,
    deleted_at_ms bigint NOT NULL,
    purge_after_ms bigint NOT NULL
);

CREATE TABLE inventory_labels (
    namespace text NOT NULL,
    unit_id text NOT NULL,
    label_key text NOT NULL,
    label_value text NOT NULL
);

CREATE TABLE issuer_keys (
    namespace_id text NOT NULL,
    id text NOT NULL,
    state text NOT NULL,
    public_key bytea NOT NULL,
    sealed bytea NOT NULL,
    created_at_ms bigint NOT NULL,
    activated_at_ms bigint NOT NULL,
    retired_at_ms bigint NOT NULL,
    deleted_at_ms bigint NOT NULL,
    CONSTRAINT issuer_keys_check CHECK (((state = ANY (ARRAY['next'::text, 'active'::text])) = (length(sealed) > 0))),
    CONSTRAINT issuer_keys_state_check CHECK ((state = ANY (ARRAY['next'::text, 'active'::text, 'retired'::text, 'deleted'::text])))
);

CREATE TABLE issuer_settings (
    namespace_id text NOT NULL,
    issuer_url text NOT NULL,
    subject_template text NOT NULL,
    allow_block_subjects boolean NOT NULL,
    include_actor_name boolean NOT NULL,
    version bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    updated_by text NOT NULL,
    copy_check text NOT NULL,
    CONSTRAINT issuer_settings_version_check CHECK ((version >= 0))
);

CREATE TABLE item_keys (
    id text NOT NULL,
    namespace_id text NOT NULL,
    scope_id text NOT NULL,
    kind text NOT NULL,
    item text NOT NULL,
    wrapper_id text NOT NULL,
    created_at_ms bigint NOT NULL,
    shredded_at_ms bigint NOT NULL,
    blob_version bigint NOT NULL,
    blob_size bigint NOT NULL,
    blob_md5 bytea NOT NULL,
    CONSTRAINT item_keys_blob_md5_check CHECK ((length(blob_md5) = 16)),
    CONSTRAINT item_keys_blob_size_check CHECK ((blob_size >= 1)),
    CONSTRAINT item_keys_blob_version_check CHECK ((blob_version >= 1)),
    CONSTRAINT item_keys_kind_check CHECK ((kind = ANY (ARRAY['state'::text, 'plan'::text, 'job-log'::text]))),
    CONSTRAINT item_keys_shredded_at_ms_check CHECK ((shredded_at_ms >= 0))
);

CREATE TABLE job_freezes (
    id text NOT NULL,
    namespace_id text NOT NULL,
    reason text NOT NULL,
    starts_at_ms bigint NOT NULL,
    ends_at_ms bigint NOT NULL,
    created_by text NOT NULL,
    ended_by text NOT NULL
);

CREATE TABLE job_log_chunks (
    log_id text NOT NULL,
    seq bigint NOT NULL,
    namespace_id text NOT NULL,
    sha256 text NOT NULL,
    size bigint NOT NULL,
    confirmed bigint NOT NULL,
    stored_size bigint DEFAULT 0 NOT NULL,
    stored_md5 bytea DEFAULT '\x'::bytea NOT NULL,
    blob_key text DEFAULT ''::text NOT NULL,
    CONSTRAINT job_log_chunks_blob_key_check CHECK ((length(blob_key) <= 512)),
    CONSTRAINT job_log_chunks_confirmed_check CHECK ((confirmed = ANY (ARRAY[(0)::bigint, (1)::bigint]))),
    CONSTRAINT job_log_chunks_seq_check CHECK ((seq >= 0)),
    CONSTRAINT job_log_chunks_size_check CHECK ((size >= 0)),
    CONSTRAINT job_log_chunks_stored_md5_check CHECK ((octet_length(stored_md5) = ANY (ARRAY[0, 16]))),
    CONSTRAINT job_log_chunks_stored_size_check CHECK ((stored_size >= 0))
);

CREATE TABLE job_logs (
    id text NOT NULL,
    namespace_id text NOT NULL,
    job_id text NOT NULL,
    phase bigint NOT NULL,
    step text NOT NULL,
    token bigint NOT NULL,
    created_at_ms bigint NOT NULL,
    CONSTRAINT job_logs_phase_check CHECK ((phase >= 0))
);

CREATE TABLE job_plans (
    id text NOT NULL,
    namespace_id text NOT NULL,
    job_id text NOT NULL,
    body text NOT NULL,
    created_at_ms bigint NOT NULL
);

CREATE TABLE jobs (
    id text NOT NULL,
    namespace_id text NOT NULL,
    unit_id text NOT NULL,
    seq bigint NOT NULL,
    state text NOT NULL,
    wait text NOT NULL,
    current_phase bigint NOT NULL,
    current_changes boolean NOT NULL,
    current_operation text NOT NULL,
    idempotency_key text NOT NULL,
    version bigint NOT NULL,
    body text NOT NULL,
    created_at_ms bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    state_changed_at_ms bigint DEFAULT 0 NOT NULL
);

CREATE TABLE local_user_resets (
    id text NOT NULL,
    user_id text NOT NULL,
    principal_id text NOT NULL,
    digest text NOT NULL,
    purpose text NOT NULL,
    created_by text NOT NULL,
    reason text DEFAULT ''::text NOT NULL,
    revoke_access_tokens boolean DEFAULT false NOT NULL,
    created_at_ms bigint NOT NULL,
    expires_at_ms bigint NOT NULL,
    CONSTRAINT local_user_resets_check CHECK ((expires_at_ms > created_at_ms)),
    CONSTRAINT local_user_resets_digest_check CHECK ((digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT local_user_resets_purpose_check CHECK ((purpose = ANY (ARRAY['set-up'::text, 'reset'::text])))
);

CREATE TABLE local_users (
    id text NOT NULL,
    principal_id text NOT NULL,
    user_name text NOT NULL,
    password_hash text DEFAULT ''::text NOT NULL,
    password_set_at_ms bigint DEFAULT 0 NOT NULL,
    change_required boolean DEFAULT false NOT NULL,
    demo boolean DEFAULT false NOT NULL,
    created_by text NOT NULL,
    created_at_ms bigint NOT NULL,
    CONSTRAINT local_users_check CHECK (((password_hash <> ''::text) OR ((NOT change_required) AND (password_set_at_ms = 0)))),
    CONSTRAINT local_users_password_hash_check CHECK (((password_hash = ''::text) OR ((length(password_hash) <= 256) AND ("left"(password_hash, 15) = '$argon2id$v=19$'::text)))),
    CONSTRAINT local_users_user_name_check CHECK ((user_name ~ '^[a-z0-9][a-z0-9._-]{0,63}$'::text))
);

CREATE TABLE manual_locks (
    kind text NOT NULL,
    object_id text NOT NULL,
    namespace text NOT NULL,
    holder text NOT NULL,
    reason text NOT NULL,
    since_ms bigint NOT NULL,
    CONSTRAINT manual_locks_holder_check CHECK ((holder <> ''::text)),
    CONSTRAINT manual_locks_kind_check CHECK ((kind = ANY (ARRAY['unit'::text, 'collection'::text, 'namespace'::text]))),
    CONSTRAINT manual_locks_reason_check CHECK (((reason <> ''::text) AND (char_length(reason) <= 500)))
);

CREATE TABLE member_removal_alerts (
    source_kind text NOT NULL,
    source_id text NOT NULL,
    alerted_at_ms bigint NOT NULL,
    CONSTRAINT member_removal_alerts_alerted_at_ms_check CHECK ((alerted_at_ms > 0)),
    CONSTRAINT member_removal_alerts_source_id_check CHECK ((source_id <> ''::text)),
    CONSTRAINT member_removal_alerts_source_kind_check CHECK ((source_kind = ANY (ARRAY['identity-provider'::text, 'scim-token'::text])))
);

CREATE TABLE member_removals (
    id text NOT NULL,
    source_kind text NOT NULL,
    source_id text NOT NULL,
    group_id text NOT NULL,
    removed bigint NOT NULL,
    members_before bigint NOT NULL,
    at_ms bigint NOT NULL,
    CONSTRAINT member_removals_at_ms_check CHECK ((at_ms > 0)),
    CONSTRAINT member_removals_group_id_check CHECK ((group_id <> ''::text)),
    CONSTRAINT member_removals_members_before_check CHECK ((members_before >= removed)),
    CONSTRAINT member_removals_removed_check CHECK ((removed >= 1)),
    CONSTRAINT member_removals_source_id_check CHECK ((source_id <> ''::text)),
    CONSTRAINT member_removals_source_kind_check CHECK ((source_kind = ANY (ARRAY['identity-provider'::text, 'scim-token'::text])))
);

CREATE TABLE name_lock_key (
    id integer NOT NULL,
    key bytea NOT NULL,
    CONSTRAINT name_lock_key_id_check CHECK ((id = 1)),
    CONSTRAINT name_lock_key_key_check CHECK ((length(key) = 32))
);

CREATE TABLE name_locks (
    scope text NOT NULL,
    name text NOT NULL,
    sub text DEFAULT ''::text NOT NULL,
    failures bigint DEFAULT 0 NOT NULL,
    locked_until_ms bigint DEFAULT 0 NOT NULL,
    level bigint DEFAULT 0 NOT NULL,
    last_failure_ms bigint DEFAULT 0 NOT NULL,
    last_lock_end_ms bigint DEFAULT 0 NOT NULL,
    reset_after_ms bigint DEFAULT 0 NOT NULL,
    CONSTRAINT name_locks_failures_check CHECK ((failures >= 0)),
    CONSTRAINT name_locks_last_failure_ms_check CHECK ((last_failure_ms >= 0)),
    CONSTRAINT name_locks_last_lock_end_ms_check CHECK ((last_lock_end_ms >= 0)),
    CONSTRAINT name_locks_level_check CHECK (((level >= 0) AND (level <= 64))),
    CONSTRAINT name_locks_locked_until_ms_check CHECK ((locked_until_ms >= 0)),
    CONSTRAINT name_locks_name_check CHECK (((length(name) >= 1) AND (length(name) <= 256))),
    CONSTRAINT name_locks_reset_after_ms_check CHECK ((reset_after_ms >= 0)),
    CONSTRAINT name_locks_scope_check CHECK ((scope ~ '^[a-z]{1,32}$'::text)),
    CONSTRAINT name_locks_sub_check CHECK ((length(sub) <= 256))
);

CREATE TABLE namespaces (
    name text NOT NULL,
    primary_installation text CONSTRAINT namespaces_home_region_not_null NOT NULL,
    failover_version bigint NOT NULL,
    created_at_ms bigint NOT NULL,
    allow_forced_writes boolean DEFAULT false NOT NULL,
    allow_unlocked_writes boolean DEFAULT false NOT NULL,
    ip_allowlist text DEFAULT ''::text NOT NULL,
    id text NOT NULL,
    output_sharing_default text DEFAULT 'nobody'::text NOT NULL,
    source_filter text DEFAULT ''::text NOT NULL,
    CONSTRAINT namespaces_id_format CHECK ((id ~~ 'ns-_%'::text)),
    CONSTRAINT namespaces_output_sharing_default_check CHECK ((output_sharing_default = ANY (ARRAY['nobody'::text, 'namespace'::text]))),
    CONSTRAINT namespaces_source_filter_check CHECK (((octet_length(source_filter) <= 1048576) AND ((source_filter = ''::text) OR pg_input_is_valid(source_filter, 'jsonb'::text))))
);

CREATE TABLE notification_cursors (
    name text NOT NULL,
    scope text NOT NULL,
    sequence bigint NOT NULL
);

CREATE TABLE notification_deliveries (
    id text NOT NULL,
    scope text NOT NULL,
    notification_id text NOT NULL,
    subscription_id text NOT NULL,
    sink_id text NOT NULL,
    contact text NOT NULL,
    principal text NOT NULL,
    state text NOT NULL,
    attempts bigint NOT NULL,
    next_at_ms bigint NOT NULL,
    last_error text NOT NULL,
    delivered_at_ms bigint NOT NULL,
    owner text NOT NULL,
    lease_until_ms bigint NOT NULL,
    version bigint NOT NULL
);

CREATE TABLE notification_sequences (
    scope text NOT NULL,
    last_sequence bigint NOT NULL
);

CREATE TABLE notification_sink_refs (
    scope text NOT NULL,
    ref text NOT NULL
);

CREATE TABLE notification_sink_secrets (
    scope text NOT NULL,
    ref text NOT NULL,
    sink_id text NOT NULL,
    "position" bigint NOT NULL,
    ciphertext bytea NOT NULL
);

CREATE TABLE notification_sinks (
    id text NOT NULL,
    scope text NOT NULL,
    kind text NOT NULL,
    name text NOT NULL,
    platform text NOT NULL,
    target text NOT NULL,
    created_by text NOT NULL,
    version bigint NOT NULL
);

CREATE TABLE notification_subscriptions (
    id text NOT NULL,
    scope text NOT NULL,
    sink_id text NOT NULL,
    types text NOT NULL,
    objects text NOT NULL,
    min_severity bigint NOT NULL,
    state text NOT NULL,
    reason text NOT NULL,
    created_by text NOT NULL,
    version bigint NOT NULL
);

CREATE TABLE notifications (
    scope text NOT NULL,
    sequence bigint NOT NULL,
    id text NOT NULL,
    type text NOT NULL,
    object text NOT NULL,
    object_id text NOT NULL,
    subject text NOT NULL,
    agent text NOT NULL,
    severity bigint NOT NULL,
    summary text NOT NULL,
    links text NOT NULL,
    attributes text NOT NULL,
    recipients text NOT NULL,
    dedupe_key text NOT NULL,
    at_ms bigint NOT NULL
);

CREATE TABLE object_context (
    namespace_id text NOT NULL,
    object_kind text NOT NULL,
    object_id text NOT NULL,
    tool text NOT NULL,
    resource_key text NOT NULL,
    source text NOT NULL,
    fields text NOT NULL,
    warning text NOT NULL,
    item_id text NOT NULL,
    item_seq bigint NOT NULL,
    resource_deleted_at_ms bigint,
    marks_seq bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    updated_by text NOT NULL,
    CONSTRAINT object_context_object_kind_check CHECK ((object_kind = ANY (ARRAY['namespace'::text, 'collection'::text, 'unit'::text, 'resource'::text, 'blueprint'::text]))),
    CONSTRAINT object_context_source_check CHECK ((source = ANY (ARRAY['api'::text, 'output'::text])))
);

CREATE TABLE object_context_owners (
    namespace_id text NOT NULL,
    owner text NOT NULL,
    object_kind text NOT NULL,
    object_id text NOT NULL,
    resource_key text NOT NULL,
    source text NOT NULL
);

CREATE TABLE oidc_providers (
    provider_id text NOT NULL,
    issuer text NOT NULL,
    created_at_ms bigint NOT NULL
);

CREATE TABLE passkey_challenges (
    digest text NOT NULL,
    kind text NOT NULL,
    principal_id text DEFAULT ''::text NOT NULL,
    pending_id text DEFAULT ''::text NOT NULL,
    session_id text DEFAULT ''::text NOT NULL,
    source text DEFAULT ''::text NOT NULL,
    source_group text DEFAULT ''::text NOT NULL,
    created_at_ms bigint NOT NULL,
    expires_at_ms bigint NOT NULL,
    CONSTRAINT passkey_challenges_check CHECK (((expires_at_ms > created_at_ms) AND ((expires_at_ms - created_at_ms) <= 300000))),
    CONSTRAINT passkey_challenges_check1 CHECK (((kind = 'sign-in'::text) = (principal_id = ''::text))),
    CONSTRAINT passkey_challenges_check2 CHECK (((kind = 'sign-in'::text) = (source <> ''::text))),
    CONSTRAINT passkey_challenges_check3 CHECK (((kind = 'sign-in'::text) = (source_group <> ''::text))),
    CONSTRAINT passkey_challenges_digest_check CHECK ((digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT passkey_challenges_kind_check CHECK ((kind = ANY (ARRAY['register'::text, 'sign-in'::text, 'second-factor'::text, 'fresh'::text]))),
    CONSTRAINT passkey_challenges_pending_id_check CHECK ((length(pending_id) <= 128)),
    CONSTRAINT passkey_challenges_principal_id_check CHECK ((length(principal_id) <= 128)),
    CONSTRAINT passkey_challenges_session_id_check CHECK ((length(session_id) <= 128)),
    CONSTRAINT passkey_challenges_source_check CHECK (((length(source) <= 64) AND (source ~ '^[ -~]*$'::text))),
    CONSTRAINT passkey_challenges_source_group_check CHECK (((length(source_group) <= 64) AND (source_group ~ '^[ -~]*$'::text)))
);

CREATE TABLE passkeys (
    id text NOT NULL,
    principal_id text NOT NULL,
    name text NOT NULL,
    credential_id bytea NOT NULL,
    public_key bytea NOT NULL,
    transports text DEFAULT ''::text NOT NULL,
    aaguid bytea NOT NULL,
    sign_count bigint DEFAULT 0 NOT NULL,
    backup_eligible boolean DEFAULT false NOT NULL,
    created_at_ms bigint NOT NULL,
    last_used_at_ms bigint DEFAULT 0 NOT NULL,
    suspended_at_ms bigint DEFAULT 0 NOT NULL,
    CONSTRAINT passkeys_aaguid_check CHECK ((length(aaguid) = 16)),
    CONSTRAINT passkeys_credential_id_check CHECK (((length(credential_id) >= 1) AND (length(credential_id) <= 1023))),
    CONSTRAINT passkeys_id_check CHECK ((("left"(id, 4) = 'pky-'::text) AND (length(id) <= 128))),
    CONSTRAINT passkeys_last_used_at_ms_check CHECK ((last_used_at_ms >= 0)),
    CONSTRAINT passkeys_name_check CHECK (((char_length(name) >= 1) AND (char_length(name) <= 100))),
    CONSTRAINT passkeys_public_key_check CHECK (((length(public_key) >= 1) AND (length(public_key) <= 4096))),
    CONSTRAINT passkeys_sign_count_check CHECK (((sign_count >= 0) AND (sign_count <= '4294967295'::bigint))),
    CONSTRAINT passkeys_suspended_at_ms_check CHECK ((suspended_at_ms >= 0)),
    CONSTRAINT passkeys_transports_check CHECK (((length(transports) <= 64) AND (transports ~ '^[a-z,-]*$'::text)))
);

CREATE TABLE person_preferences (
    principal_id text NOT NULL,
    document text NOT NULL,
    updated_at_ms bigint NOT NULL,
    CONSTRAINT person_preferences_document_check CHECK (((char_length(document) <= 4096) AND (jsonb_typeof((document)::jsonb) = 'object'::text)))
);

CREATE TABLE placement_rules (
    id text NOT NULL,
    namespace text NOT NULL,
    "position" integer NOT NULL,
    tool text NOT NULL,
    project text NOT NULL,
    stack text NOT NULL,
    collection_key text NOT NULL,
    created_by text NOT NULL,
    created_at_ms bigint NOT NULL,
    CONSTRAINT placement_rules_position_check CHECK (("position" >= 1))
);

CREATE TABLE principal_groups (
    id text NOT NULL,
    name text NOT NULL,
    name_key text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    source text DEFAULT 'local'::text NOT NULL,
    created_at_ms bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    deleted_at_ms bigint,
    closed_to_identity_provider boolean DEFAULT false NOT NULL
);

CREATE TABLE principal_session_generations (
    principal_id text NOT NULL,
    generation bigint NOT NULL,
    CONSTRAINT principal_session_generations_generation_check CHECK ((generation >= 2))
);

CREATE TABLE principals (
    id text NOT NULL,
    subject text NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    kind text NOT NULL,
    source text DEFAULT 'local'::text NOT NULL,
    created_at_ms bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    deleted_at_ms bigint,
    disabled_at_ms bigint,
    last_active_at_ms bigint DEFAULT 0 NOT NULL,
    last_sign_in_at_ms bigint,
    namespace text DEFAULT ''::text NOT NULL,
    break_glass boolean DEFAULT false NOT NULL
);

CREATE TABLE profile_revisions (
    namespace_id text NOT NULL,
    profile_id text NOT NULL,
    number bigint NOT NULL,
    document text NOT NULL,
    document_sha256 text NOT NULL,
    author text NOT NULL,
    created_at_ms bigint NOT NULL,
    reason text NOT NULL,
    CONSTRAINT profile_revisions_number_check CHECK ((number >= 1))
);

CREATE TABLE profiles (
    namespace_id text NOT NULL,
    id text NOT NULL,
    name text NOT NULL,
    latest bigint NOT NULL,
    created_by text NOT NULL,
    created_at_ms bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    deleted_at_ms bigint NOT NULL,
    CONSTRAINT profiles_latest_check CHECK ((latest >= 1))
);

CREATE TABLE registry_keys (
    namespace_id text NOT NULL,
    fingerprint text NOT NULL,
    armored_key text NOT NULL,
    added_by text NOT NULL,
    added_at_ms bigint NOT NULL
);

CREATE TABLE registry_releases (
    namespace_id text NOT NULL,
    kind text NOT NULL,
    name text NOT NULL,
    system text NOT NULL,
    version text NOT NULL,
    sums_file text NOT NULL,
    sums_sha256 text NOT NULL,
    signature_file text NOT NULL,
    signature_sha256 text NOT NULL,
    key_fingerprint text NOT NULL,
    protocols text NOT NULL,
    published_by text NOT NULL,
    archives_json text NOT NULL,
    published_at_ms bigint NOT NULL,
    stored_files_json text DEFAULT ''::text NOT NULL,
    CONSTRAINT registry_releases_stored_files_check CHECK (((octet_length(stored_files_json) <= 1048576) AND ((stored_files_json = ''::text) OR pg_input_is_valid(stored_files_json, 'jsonb'::text))))
);

CREATE TABLE roles (
    id text NOT NULL,
    name text NOT NULL,
    name_key text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    actions text NOT NULL,
    created_at_ms bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    deleted_at_ms bigint
);

CREATE TABLE runner_claims (
    operation_id text NOT NULL,
    token bigint NOT NULL,
    claimed_at_ms bigint NOT NULL,
    job_id text DEFAULT ''::text NOT NULL,
    phase bigint DEFAULT 0 NOT NULL
);

CREATE TABLE runner_handoff_tokens (
    sha256 text NOT NULL,
    operation_id text NOT NULL,
    token bigint NOT NULL,
    runner_id text NOT NULL,
    issued_at_ms bigint NOT NULL,
    expires_at_ms bigint NOT NULL,
    used_at_ms bigint NOT NULL
);

CREATE TABLE runner_join_tokens (
    id text NOT NULL,
    namespace_id text NOT NULL,
    pool_id text NOT NULL,
    sha256 text NOT NULL,
    created_by text NOT NULL,
    created_at_ms bigint NOT NULL,
    expires_at_ms bigint NOT NULL,
    used_at_ms bigint NOT NULL,
    used_by text NOT NULL,
    revoked_at_ms bigint NOT NULL
);

CREATE TABLE runner_nonces (
    subject text NOT NULL,
    nonce text NOT NULL,
    expires_at_ms bigint NOT NULL
);

CREATE TABLE runner_pools (
    id text NOT NULL,
    namespace_id text NOT NULL,
    name text NOT NULL,
    purpose text NOT NULL,
    lease_ms bigint NOT NULL,
    concurrency bigint NOT NULL,
    isolation text NOT NULL,
    git_mode text NOT NULL,
    git_helper text NOT NULL,
    created_at_ms bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    CONSTRAINT runner_pools_purpose_check CHECK ((purpose = ANY (ARRAY['jobs'::text, 'sweeps'::text])))
);

CREATE TABLE runner_sessions (
    id text NOT NULL,
    operation_id text NOT NULL,
    token bigint NOT NULL,
    runner_id text NOT NULL,
    key_public bytea NOT NULL,
    key_created_at_ms bigint NOT NULL,
    created_at_ms bigint NOT NULL
);

CREATE TABLE runners (
    id text NOT NULL,
    namespace_id text NOT NULL,
    pool_id text NOT NULL,
    name text NOT NULL,
    kind text NOT NULL,
    isolation text NOT NULL,
    labels text NOT NULL,
    release text NOT NULL,
    key_public bytea NOT NULL,
    key_created_at_ms bigint NOT NULL,
    previous_key_public bytea NOT NULL,
    previous_key_created_at_ms bigint NOT NULL,
    rotated_at_ms bigint NOT NULL,
    joined_with text NOT NULL,
    draining boolean NOT NULL,
    retired_at_ms bigint NOT NULL,
    last_seen_at_ms bigint NOT NULL,
    joined_at_ms bigint NOT NULL,
    CONSTRAINT runners_kind_check CHECK ((kind = ANY (ARRAY['kubernetes'::text, 'machine'::text])))
);

CREATE TABLE saved_views (
    id text NOT NULL,
    namespace_id text NOT NULL,
    owner text NOT NULL,
    name text NOT NULL,
    query_json text NOT NULL,
    created_at_ms bigint NOT NULL
);

CREATE TABLE secret_keys (
    namespace text NOT NULL,
    stack_id text NOT NULL,
    wrapper_id text NOT NULL,
    ciphertext bytea NOT NULL,
    created_at_ms bigint NOT NULL
);

CREATE TABLE self_service_blueprint_versions (
    namespace_id text NOT NULL,
    name text NOT NULL,
    number bigint NOT NULL,
    draft_version bigint NOT NULL,
    document_json text NOT NULL,
    expr_version text NOT NULL,
    published_by text NOT NULL,
    published_at_ms bigint NOT NULL,
    retired bigint NOT NULL,
    CONSTRAINT self_service_blueprint_versions_draft_version_check CHECK ((draft_version >= 1)),
    CONSTRAINT self_service_blueprint_versions_number_check CHECK ((number >= 1)),
    CONSTRAINT self_service_blueprint_versions_retired_check CHECK ((retired = ANY (ARRAY[(0)::bigint, (1)::bigint])))
);

CREATE TABLE self_service_blueprints (
    namespace_id text NOT NULL,
    name text NOT NULL,
    draft_version bigint NOT NULL,
    draft_json text NOT NULL,
    draft_updated_at_ms bigint NOT NULL,
    CONSTRAINT self_service_blueprints_draft_version_check CHECK ((draft_version >= 1))
);

CREATE TABLE session_policy (
    id bigint NOT NULL,
    idle_ms bigint NOT NULL,
    max_length_ms bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    token_freshness_ms bigint,
    CONSTRAINT session_policy_id_check CHECK ((id = 1))
);

CREATE TABLE sessions (
    id text NOT NULL,
    principal_id text NOT NULL,
    digest text NOT NULL,
    provider text NOT NULL,
    created_at_ms bigint NOT NULL,
    last_seen_at_ms bigint NOT NULL,
    auth_time_ms bigint NOT NULL,
    ended_at_ms bigint,
    end_reason text DEFAULT ''::text NOT NULL,
    source_ip text DEFAULT ''::text NOT NULL,
    user_agent text DEFAULT ''::text NOT NULL,
    generation bigint DEFAULT 0 NOT NULL,
    CONSTRAINT sessions_generation_check CHECK ((generation >= 0))
);

CREATE TABLE settings (
    scope_id text NOT NULL,
    key text NOT NULL,
    value text NOT NULL,
    updated_at_ms bigint NOT NULL,
    CONSTRAINT settings_key_check CHECK (((char_length(key) >= 1) AND (char_length(key) <= 64))),
    CONSTRAINT settings_value_check CHECK ((char_length(value) <= 1024))
);

CREATE TABLE settings_state (
    scope_id text NOT NULL,
    revision bigint NOT NULL,
    static_digest text NOT NULL,
    CONSTRAINT settings_state_revision_check CHECK ((revision >= 0))
);

CREATE TABLE sign_in_failures (
    source text NOT NULL,
    window_end_ms bigint NOT NULL,
    count bigint NOT NULL
);

CREATE TABLE sign_in_providers (
    provider_id text NOT NULL,
    issuer text NOT NULL,
    display_name text NOT NULL,
    client_id text NOT NULL,
    client_secret bytea NOT NULL,
    scopes text NOT NULL,
    auto_create boolean NOT NULL,
    enabled boolean NOT NULL,
    created_at_ms bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    version bigint NOT NULL,
    mode text DEFAULT 'oidc'::text NOT NULL,
    authorization_url text DEFAULT ''::text NOT NULL,
    token_url text DEFAULT ''::text NOT NULL,
    userinfo_url text DEFAULT ''::text NOT NULL,
    subject_claim text DEFAULT ''::text NOT NULL,
    name_claim text DEFAULT ''::text NOT NULL,
    username_claim text DEFAULT ''::text NOT NULL,
    email_claim text DEFAULT ''::text NOT NULL,
    CONSTRAINT sign_in_providers_mode_check CHECK ((mode = ANY (ARRAY['oidc'::text, 'oauth'::text])))
);

CREATE TABLE sign_in_requests (
    digest text NOT NULL,
    provider text NOT NULL,
    nonce text NOT NULL,
    verifier text NOT NULL,
    return_path text NOT NULL,
    created_at_ms bigint NOT NULL,
    expires_at_ms bigint NOT NULL,
    max_age_ms bigint
);

CREATE TABLE stack_tags (
    stack_id text NOT NULL,
    name text NOT NULL,
    value text NOT NULL
);

CREATE TABLE stacks (
    namespace text NOT NULL,
    project text NOT NULL,
    name text NOT NULL,
    version bigint NOT NULL,
    created_at_ms bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    id text NOT NULL,
    deleted_at_ms bigint,
    deleted_name text
);

CREATE TABLE state_versions (
    id text NOT NULL,
    workspace_id text NOT NULL,
    status text NOT NULL,
    serial bigint NOT NULL,
    lineage text NOT NULL,
    md5 bytea NOT NULL,
    size bigint NOT NULL,
    content_key text DEFAULT ''::text NOT NULL,
    outputs bytea,
    force boolean DEFAULT false NOT NULL,
    upload_digest bytea NOT NULL,
    upload_expires_at_ms bigint NOT NULL,
    holder text DEFAULT ''::text NOT NULL,
    lock_id text DEFAULT ''::text NOT NULL,
    created_at_ms bigint NOT NULL,
    finalized_at_ms bigint DEFAULT 0 NOT NULL,
    restored_from text DEFAULT ''::text NOT NULL,
    sealed boolean DEFAULT false NOT NULL,
    sensitive_marks bytea,
    fenced_by text DEFAULT ''::text NOT NULL,
    unlocked_write boolean DEFAULT false NOT NULL,
    origin_job text DEFAULT ''::text NOT NULL
);

CREATE TABLE temp_admins (
    principal_id text NOT NULL,
    subject text NOT NULL,
    expires_at_ms bigint NOT NULL,
    created_at_ms bigint NOT NULL,
    created_by text NOT NULL,
    reason text NOT NULL,
    ended_at_ms bigint,
    review_opened_at_ms bigint
);

CREATE TABLE triggers (
    namespace_id text NOT NULL,
    id text NOT NULL,
    unit_tool text NOT NULL,
    unit_id text NOT NULL,
    kind text NOT NULL,
    preset text NOT NULL,
    cron text NOT NULL,
    source_namespace text NOT NULL,
    source_tool text NOT NULL,
    source_unit_id text NOT NULL,
    enabled bigint NOT NULL,
    last_saver_subject text NOT NULL,
    last_saver_id text NOT NULL,
    created_at_ms bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    next_slot_ms bigint DEFAULT 0 NOT NULL,
    CONSTRAINT triggers_check CHECK (((next_slot_ms >= 0) AND ((kind <> 'schedule'::text) OR (enabled = 0) OR (next_slot_ms > 0)))),
    CONSTRAINT triggers_enabled_check CHECK ((enabled = ANY (ARRAY[(0)::bigint, (1)::bigint]))),
    CONSTRAINT triggers_kind_check CHECK ((kind = ANY (ARRAY['review-request-push'::text, 'schedule'::text, 'follows'::text])))
);

CREATE TABLE trust_rules (
    id text NOT NULL,
    namespace text NOT NULL,
    name text NOT NULL,
    issuer text NOT NULL,
    audience text NOT NULL,
    key_source text NOT NULL,
    static_keys text NOT NULL,
    conditions_json text NOT NULL,
    principal_id text NOT NULL,
    created_by text NOT NULL,
    created_at_ms bigint NOT NULL,
    CONSTRAINT trust_rules_key_source_check CHECK ((key_source = ANY (ARRAY['discovery'::text, 'static'::text])))
);

CREATE TABLE unit_defaults (
    namespace_id text NOT NULL,
    pool text NOT NULL,
    version bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    CONSTRAINT unit_defaults_version_check CHECK ((version >= 1))
);

CREATE TABLE unit_settings (
    unit_id text NOT NULL,
    namespace_id text NOT NULL,
    version bigint NOT NULL,
    body text NOT NULL,
    updated_at_ms bigint NOT NULL,
    CONSTRAINT unit_settings_version_check CHECK ((version >= 1))
);

CREATE TABLE unit_state (
    unit_id text NOT NULL,
    namespace_id text NOT NULL,
    version_id text NOT NULL DEFAULT '',
    final_version_id text NOT NULL DEFAULT '',
    final_commit text NOT NULL DEFAULT '',
    final_job text NOT NULL DEFAULT '',
    final_at_ms bigint NOT NULL DEFAULT 0
);

CREATE TABLE update_events (
    update_id text NOT NULL,
    sequence bigint NOT NULL,
    timestamp_ms bigint NOT NULL,
    payload bytea NOT NULL,
    stack_id text NOT NULL
);

CREATE TABLE update_journal (
    stack_id text NOT NULL,
    update_id text NOT NULL,
    arrival bigint NOT NULL,
    blob_key text NOT NULL,
    sha256 bytea NOT NULL,
    size bigint NOT NULL,
    lowest_sequence bigint NOT NULL,
    sequence_ids text NOT NULL,
    operation_ids text NOT NULL,
    md5 bytea NOT NULL
);

CREATE TABLE update_journal_refusals (
    stack_id text NOT NULL,
    update_id text NOT NULL,
    arrival bigint NOT NULL,
    sequence_ids text NOT NULL,
    status bigint NOT NULL,
    reason text NOT NULL
);

CREATE TABLE update_journal_sequences (
    stack_id text NOT NULL,
    update_id text NOT NULL,
    sequence bigint NOT NULL,
    arrival bigint NOT NULL,
    sha256 bytea NOT NULL
);

CREATE TABLE updates (
    id text NOT NULL,
    kind text NOT NULL,
    dry_run boolean NOT NULL,
    status text NOT NULL,
    version bigint NOT NULL,
    revision bigint NOT NULL,
    lease_digest bytea NOT NULL,
    lease_expires_at_ms bigint NOT NULL,
    created_at_ms bigint NOT NULL,
    started_at_ms bigint NOT NULL,
    ended_at_ms bigint NOT NULL,
    stack_id text NOT NULL,
    created_by text DEFAULT ''::text NOT NULL,
    restored_from bigint DEFAULT 0 NOT NULL,
    journal_version bigint DEFAULT 0 NOT NULL,
    journal_closed boolean DEFAULT false NOT NULL,
    token_id text DEFAULT ''::text NOT NULL,
    journal_records_changed boolean DEFAULT false NOT NULL,
    job_id text DEFAULT ''::text NOT NULL,
    git_head text DEFAULT ''::text NOT NULL
);

CREATE TABLE used_tickets (
    provider_id text NOT NULL,
    ticket_digest text NOT NULL,
    until_ms bigint NOT NULL,
    CONSTRAINT used_tickets_provider_id_check CHECK (((length(provider_id) >= 1) AND (length(provider_id) <= 256))),
    CONSTRAINT used_tickets_ticket_digest_check CHECK ((ticket_digest ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT used_tickets_until_ms_check CHECK ((until_ms > 0))
);

CREATE TABLE workspace_blame_cursors (
    workspace_id text NOT NULL,
    ordinal bigint NOT NULL,
    CONSTRAINT workspace_blame_cursors_ordinal_check CHECK ((ordinal >= 1))
);

CREATE TABLE workspace_blame_items (
    workspace_id text NOT NULL,
    state_version_id text NOT NULL,
    ordinal bigint NOT NULL,
    opaque boolean NOT NULL,
    lineage_changed boolean NOT NULL,
    resource_count bigint NOT NULL,
    CONSTRAINT workspace_blame_items_ordinal_check CHECK ((ordinal >= 1)),
    CONSTRAINT workspace_blame_items_resource_count_check CHECK ((resource_count >= 0))
);

CREATE TABLE workspace_resource_changes (
    workspace_id text NOT NULL,
    state_version_id text NOT NULL,
    address text NOT NULL,
    ordinal bigint NOT NULL,
    kind text NOT NULL,
    at_ms bigint NOT NULL,
    by_subject text NOT NULL,
    by_token_id text NOT NULL,
    by_job_id text NOT NULL,
    changed_paths bigint NOT NULL,
    restored boolean NOT NULL,
    forced boolean NOT NULL,
    lineage_changed boolean NOT NULL,
    CONSTRAINT workspace_resource_changes_changed_paths_check CHECK ((changed_paths >= 0)),
    CONSTRAINT workspace_resource_changes_kind_check CHECK ((kind = ANY (ARRAY['created'::text, 'updated'::text, 'replaced'::text, 'deleted'::text])))
);

CREATE TABLE workspaces (
    id text NOT NULL,
    namespace text NOT NULL,
    name text NOT NULL,
    revision bigint NOT NULL,
    lock_id text DEFAULT ''::text NOT NULL,
    lock_holder text DEFAULT ''::text NOT NULL,
    lock_reason text DEFAULT ''::text NOT NULL,
    locked_at_ms bigint DEFAULT 0 NOT NULL,
    current_state_version_id text DEFAULT ''::text NOT NULL,
    created_at_ms bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    allow_forced_writes boolean DEFAULT false NOT NULL,
    allow_unlocked_writes boolean DEFAULT false NOT NULL,
    deleted_at_ms bigint,
    deleted_name text,
    last_release_lock_id text DEFAULT ''::text NOT NULL,
    last_release_holder text DEFAULT ''::text NOT NULL,
    last_release_by text DEFAULT ''::text NOT NULL,
    last_release_kind text DEFAULT ''::text NOT NULL,
    last_release_at_ms bigint DEFAULT 0 NOT NULL,
    CONSTRAINT workspaces_last_release_kind_check CHECK ((last_release_kind = ANY (ARRAY[''::text, 'normal'::text, 'forced'::text])))
);

INSERT INTO access_token_policy (id, max_lifetime_ms, inactivity_ms, disable_tokens, disable_accounts, disable_break_glass, updated_at_ms, exchange_lifetime_ms) VALUES (1, 31536000000, 7776000000, true, true, false, 0, 3600000);

INSERT INTO audit_chain_state (id, state, changed_at_ms, changed_by, pending, entered_maintenance, log_replaced, migrating_at_ms) VALUES (1, 'on', 0, '', false, false, false, 0);

INSERT INTO session_policy (id, idle_ms, max_length_ms, updated_at_ms, token_freshness_ms) VALUES (1, 900000, 43200000, 0, NULL);

ALTER TABLE ONLY access_token_policy
    ADD CONSTRAINT access_token_policy_pkey PRIMARY KEY (id);

ALTER TABLE ONLY access_tokens
    ADD CONSTRAINT access_tokens_pkey PRIMARY KEY (id);

ALTER TABLE ONLY audit_chain_check
    ADD CONSTRAINT audit_chain_check_pkey PRIMARY KEY (id);

ALTER TABLE ONLY audit_chain_mark
    ADD CONSTRAINT audit_chain_mark_pkey PRIMARY KEY (scope);

ALTER TABLE ONLY audit_chain_state
    ADD CONSTRAINT audit_chain_state_pkey PRIMARY KEY (id);

ALTER TABLE ONLY audit_entries
    ADD CONSTRAINT audit_entries_pkey PRIMARY KEY (namespace, sequence);

ALTER TABLE ONLY audit_sink_cursors
    ADD CONSTRAINT audit_sink_cursors_pkey PRIMARY KEY (sink, namespace);

ALTER TABLE ONLY audit_sink_leases
    ADD CONSTRAINT audit_sink_leases_pkey PRIMARY KEY (sink);

ALTER TABLE ONLY blob_purges
    ADD CONSTRAINT blob_purges_pkey PRIMARY KEY (tool, unit_id);

ALTER TABLE ONLY break_glass_reviews
    ADD CONSTRAINT break_glass_reviews_pkey PRIMARY KEY (id);

ALTER TABLE ONLY checkpoints
    ADD CONSTRAINT checkpoints_pkey PRIMARY KEY (stack_id, update_id);

ALTER TABLE ONLY collection_members
    ADD CONSTRAINT collection_members_pkey PRIMARY KEY (unit_id);

ALTER TABLE ONLY collections
    ADD CONSTRAINT collections_id_key UNIQUE (id);

ALTER TABLE ONLY collections
    ADD CONSTRAINT collections_pkey PRIMARY KEY (namespace, name_key);

ALTER TABLE ONLY drift_results
    ADD CONSTRAINT drift_results_pkey PRIMARY KEY (unit_id);

ALTER TABLE ONLY durable_heartbeats
    ADD CONSTRAINT durable_heartbeats_pkey PRIMARY KEY (owner);

ALTER TABLE ONLY durable_operations
    ADD CONSTRAINT durable_operations_pkey PRIMARY KEY (id);

ALTER TABLE ONLY durable_operations
    ADD CONSTRAINT durable_operations_start_key_key UNIQUE (start_key);

ALTER TABLE ONLY durable_schedules
    ADD CONSTRAINT durable_schedules_pkey PRIMARY KEY (name);

ALTER TABLE ONLY durable_steps
    ADD CONSTRAINT durable_steps_operation_id_seq_key UNIQUE (operation_id, seq);

ALTER TABLE ONLY durable_steps
    ADD CONSTRAINT durable_steps_pkey PRIMARY KEY (operation_id, name);

ALTER TABLE ONLY forge_account_links
    ADD CONSTRAINT forge_account_links_pkey PRIMARY KEY (kind, host, account_id);

ALTER TABLE ONLY forge_link_requests
    ADD CONSTRAINT forge_link_requests_pkey PRIMARY KEY (token_sha256);

ALTER TABLE ONLY gate_objects
    ADD CONSTRAINT gate_objects_pkey PRIMARY KEY (namespace_id, scope_key, kind, id);

ALTER TABLE ONLY gate_records
    ADD CONSTRAINT gate_records_pkey PRIMARY KEY (id);

ALTER TABLE ONLY gate_references
    ADD CONSTRAINT gate_references_pkey PRIMARY KEY (ref_kind, ref_id, namespace_id, scope_key, owner_kind, owner_id);

ALTER TABLE ONLY git_host_app_registrations
    ADD CONSTRAINT git_host_app_registrations_hook_id_key UNIQUE (hook_id);

ALTER TABLE ONLY git_host_app_registrations
    ADD CONSTRAINT git_host_app_registrations_pkey PRIMARY KEY (kind);

ALTER TABLE ONLY git_host_connections
    ADD CONSTRAINT git_host_connections_pkey PRIMARY KEY (namespace_id, id);

ALTER TABLE ONLY git_host_installations
    ADD CONSTRAINT git_host_installations_pkey PRIMARY KEY (installation_id);

ALTER TABLE ONLY grants
    ADD CONSTRAINT grants_pkey PRIMARY KEY (id);

ALTER TABLE ONLY group_mappings
    ADD CONSTRAINT group_mappings_pkey PRIMARY KEY (source, source_id, claim_value, group_id);

ALTER TABLE ONLY group_members
    ADD CONSTRAINT group_members_pkey PRIMARY KEY (group_id, principal_id, added_at_ms);

ALTER TABLE ONLY idempotency_records
    ADD CONSTRAINT idempotency_records_pkey PRIMARY KEY (principal, method, path, idem_key);

ALTER TABLE ONLY idempotency_server_key
    ADD CONSTRAINT idempotency_server_key_pkey PRIMARY KEY (id);

ALTER TABLE ONLY import_ledger
    ADD CONSTRAINT import_ledger_pkey PRIMARY KEY (namespace_id, import_id, import_unit_id, version);

ALTER TABLE ONLY import_units
    ADD CONSTRAINT import_units_pkey PRIMARY KEY (namespace_id, import_id, id);

ALTER TABLE ONLY import_upload_commits
    ADD CONSTRAINT import_upload_commits_pkey PRIMARY KEY (namespace_id, import_id, unit_dir, sequence);

ALTER TABLE ONLY imports
    ADD CONSTRAINT imports_pkey PRIMARY KEY (namespace_id, id);

ALTER TABLE ONLY imports_purged
    ADD CONSTRAINT imports_purged_pkey PRIMARY KEY (namespace_id, id);

ALTER TABLE ONLY installation_secret_keys
    ADD CONSTRAINT installation_secret_keys_pkey PRIMARY KEY (scope_id);

ALTER TABLE ONLY inventory_change_heads
    ADD CONSTRAINT inventory_change_heads_pkey PRIMARY KEY (namespace_id);

ALTER TABLE ONLY inventory_changes
    ADD CONSTRAINT inventory_changes_pkey PRIMARY KEY (namespace_id, seq);

ALTER TABLE ONLY inventory_deleted
    ADD CONSTRAINT inventory_deleted_pkey PRIMARY KEY (tool, unit_id);

ALTER TABLE ONLY inventory_labels
    ADD CONSTRAINT inventory_labels_pkey PRIMARY KEY (unit_id, label_key);

ALTER TABLE ONLY inventory
    ADD CONSTRAINT inventory_pkey PRIMARY KEY (namespace, tool, name);

ALTER TABLE ONLY issuer_keys
    ADD CONSTRAINT issuer_keys_pkey PRIMARY KEY (namespace_id, id);

ALTER TABLE ONLY issuer_settings
    ADD CONSTRAINT issuer_settings_pkey PRIMARY KEY (namespace_id);

ALTER TABLE ONLY item_keys
    ADD CONSTRAINT item_keys_pkey PRIMARY KEY (id);

ALTER TABLE ONLY job_freezes
    ADD CONSTRAINT job_freezes_pkey PRIMARY KEY (id);

ALTER TABLE ONLY job_log_chunks
    ADD CONSTRAINT job_log_chunks_pkey PRIMARY KEY (log_id, seq);

ALTER TABLE ONLY job_logs
    ADD CONSTRAINT job_logs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY job_plans
    ADD CONSTRAINT job_plans_pkey PRIMARY KEY (id);

ALTER TABLE ONLY jobs
    ADD CONSTRAINT jobs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY local_user_resets
    ADD CONSTRAINT local_user_resets_digest_key UNIQUE (digest);

ALTER TABLE ONLY local_user_resets
    ADD CONSTRAINT local_user_resets_pkey PRIMARY KEY (id);

ALTER TABLE ONLY local_user_resets
    ADD CONSTRAINT local_user_resets_user_id_key UNIQUE (user_id);

ALTER TABLE ONLY local_users
    ADD CONSTRAINT local_users_id_principal_id_key UNIQUE (id, principal_id);

ALTER TABLE ONLY local_users
    ADD CONSTRAINT local_users_pkey PRIMARY KEY (id);

ALTER TABLE ONLY local_users
    ADD CONSTRAINT local_users_principal_id_key UNIQUE (principal_id);

ALTER TABLE ONLY local_users
    ADD CONSTRAINT local_users_user_name_key UNIQUE (user_name);

ALTER TABLE ONLY manual_locks
    ADD CONSTRAINT manual_locks_pkey PRIMARY KEY (kind, object_id);

ALTER TABLE ONLY member_removal_alerts
    ADD CONSTRAINT member_removal_alerts_pkey PRIMARY KEY (source_kind, source_id);

ALTER TABLE ONLY member_removals
    ADD CONSTRAINT member_removals_pkey PRIMARY KEY (id);

ALTER TABLE ONLY name_lock_key
    ADD CONSTRAINT name_lock_key_pkey PRIMARY KEY (id);

ALTER TABLE ONLY name_locks
    ADD CONSTRAINT name_locks_pkey PRIMARY KEY (scope, name, sub);

ALTER TABLE ONLY namespaces
    ADD CONSTRAINT namespaces_pkey PRIMARY KEY (name);

ALTER TABLE ONLY notification_cursors
    ADD CONSTRAINT notification_cursors_pkey PRIMARY KEY (name, scope);

ALTER TABLE ONLY notification_deliveries
    ADD CONSTRAINT notification_deliveries_pkey PRIMARY KEY (id);

ALTER TABLE ONLY notification_deliveries
    ADD CONSTRAINT notification_deliveries_scope_notification_id_subscription__key UNIQUE (scope, notification_id, subscription_id, contact);

ALTER TABLE ONLY notification_sequences
    ADD CONSTRAINT notification_sequences_pkey PRIMARY KEY (scope);

ALTER TABLE ONLY notification_sink_refs
    ADD CONSTRAINT notification_sink_refs_pkey PRIMARY KEY (scope, ref);

ALTER TABLE ONLY notification_sink_secrets
    ADD CONSTRAINT notification_sink_secrets_pkey PRIMARY KEY (scope, ref);

ALTER TABLE ONLY notification_sinks
    ADD CONSTRAINT notification_sinks_pkey PRIMARY KEY (id);

ALTER TABLE ONLY notification_subscriptions
    ADD CONSTRAINT notification_subscriptions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY notifications
    ADD CONSTRAINT notifications_id_key UNIQUE (id);

ALTER TABLE ONLY notifications
    ADD CONSTRAINT notifications_pkey PRIMARY KEY (scope, sequence);

ALTER TABLE ONLY notifications
    ADD CONSTRAINT notifications_scope_dedupe_key_key UNIQUE (scope, dedupe_key);

ALTER TABLE ONLY object_context_owners
    ADD CONSTRAINT object_context_owners_pkey PRIMARY KEY (namespace_id, owner, object_kind, object_id, resource_key, source);

ALTER TABLE ONLY object_context
    ADD CONSTRAINT object_context_pkey PRIMARY KEY (namespace_id, object_kind, object_id, resource_key, source);

ALTER TABLE ONLY oidc_providers
    ADD CONSTRAINT oidc_providers_pkey PRIMARY KEY (provider_id);

ALTER TABLE ONLY passkey_challenges
    ADD CONSTRAINT passkey_challenges_pkey PRIMARY KEY (digest);

ALTER TABLE ONLY passkeys
    ADD CONSTRAINT passkeys_credential_id_key UNIQUE (credential_id);

ALTER TABLE ONLY passkeys
    ADD CONSTRAINT passkeys_pkey PRIMARY KEY (id);

ALTER TABLE ONLY person_preferences
    ADD CONSTRAINT person_preferences_pkey PRIMARY KEY (principal_id);

ALTER TABLE ONLY placement_rules
    ADD CONSTRAINT placement_rules_pkey PRIMARY KEY (id);

ALTER TABLE ONLY principal_groups
    ADD CONSTRAINT principal_groups_pkey PRIMARY KEY (id);

ALTER TABLE ONLY principal_session_generations
    ADD CONSTRAINT principal_session_generations_pkey PRIMARY KEY (principal_id);

ALTER TABLE ONLY principals
    ADD CONSTRAINT principals_pkey PRIMARY KEY (id);

ALTER TABLE ONLY profile_revisions
    ADD CONSTRAINT profile_revisions_pkey PRIMARY KEY (namespace_id, profile_id, number);

ALTER TABLE ONLY profiles
    ADD CONSTRAINT profiles_pkey PRIMARY KEY (namespace_id, id);

ALTER TABLE ONLY registry_keys
    ADD CONSTRAINT registry_keys_pkey PRIMARY KEY (namespace_id, fingerprint);

ALTER TABLE ONLY registry_releases
    ADD CONSTRAINT registry_releases_pkey PRIMARY KEY (namespace_id, kind, name, system, version);

ALTER TABLE ONLY roles
    ADD CONSTRAINT roles_pkey PRIMARY KEY (id);

ALTER TABLE ONLY runner_claims
    ADD CONSTRAINT runner_claims_pkey PRIMARY KEY (operation_id, token);

ALTER TABLE ONLY runner_handoff_tokens
    ADD CONSTRAINT runner_handoff_tokens_operation_id_token_key UNIQUE (operation_id, token);

ALTER TABLE ONLY runner_handoff_tokens
    ADD CONSTRAINT runner_handoff_tokens_pkey PRIMARY KEY (sha256);

ALTER TABLE ONLY runner_join_tokens
    ADD CONSTRAINT runner_join_tokens_pkey PRIMARY KEY (id);

ALTER TABLE ONLY runner_join_tokens
    ADD CONSTRAINT runner_join_tokens_sha256_key UNIQUE (sha256);

ALTER TABLE ONLY runner_nonces
    ADD CONSTRAINT runner_nonces_pkey PRIMARY KEY (subject, nonce);

ALTER TABLE ONLY runner_pools
    ADD CONSTRAINT runner_pools_namespace_id_name_key UNIQUE (namespace_id, name);

ALTER TABLE ONLY runner_pools
    ADD CONSTRAINT runner_pools_pkey PRIMARY KEY (id);

ALTER TABLE ONLY runner_sessions
    ADD CONSTRAINT runner_sessions_operation_id_token_key UNIQUE (operation_id, token);

ALTER TABLE ONLY runner_sessions
    ADD CONSTRAINT runner_sessions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY runners
    ADD CONSTRAINT runners_pkey PRIMARY KEY (id);

ALTER TABLE ONLY saved_views
    ADD CONSTRAINT saved_views_pkey PRIMARY KEY (id);

ALTER TABLE ONLY secret_keys
    ADD CONSTRAINT secret_keys_pkey PRIMARY KEY (namespace, stack_id);

ALTER TABLE ONLY self_service_blueprint_versions
    ADD CONSTRAINT self_service_blueprint_versions_pkey PRIMARY KEY (namespace_id, name, number);

ALTER TABLE ONLY self_service_blueprints
    ADD CONSTRAINT self_service_blueprints_pkey PRIMARY KEY (namespace_id, name);

ALTER TABLE ONLY session_policy
    ADD CONSTRAINT session_policy_pkey PRIMARY KEY (id);

ALTER TABLE ONLY sessions
    ADD CONSTRAINT sessions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY settings
    ADD CONSTRAINT settings_pkey PRIMARY KEY (scope_id, key);

ALTER TABLE ONLY settings_state
    ADD CONSTRAINT settings_state_pkey PRIMARY KEY (scope_id);

ALTER TABLE ONLY sign_in_failures
    ADD CONSTRAINT sign_in_failures_pkey PRIMARY KEY (source, window_end_ms);

ALTER TABLE ONLY sign_in_providers
    ADD CONSTRAINT sign_in_providers_pkey PRIMARY KEY (provider_id);

ALTER TABLE ONLY sign_in_requests
    ADD CONSTRAINT sign_in_requests_pkey PRIMARY KEY (digest);

ALTER TABLE ONLY stack_tags
    ADD CONSTRAINT stack_tags_pkey PRIMARY KEY (stack_id, name);

ALTER TABLE ONLY stacks
    ADD CONSTRAINT stacks_pkey PRIMARY KEY (namespace, project, name);

ALTER TABLE ONLY state_versions
    ADD CONSTRAINT state_versions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY temp_admins
    ADD CONSTRAINT temp_admins_pkey PRIMARY KEY (principal_id);

ALTER TABLE ONLY triggers
    ADD CONSTRAINT triggers_pkey PRIMARY KEY (namespace_id, id);

ALTER TABLE ONLY trust_rules
    ADD CONSTRAINT trust_rules_pkey PRIMARY KEY (id);

ALTER TABLE ONLY unit_defaults
    ADD CONSTRAINT unit_defaults_pkey PRIMARY KEY (namespace_id);

ALTER TABLE ONLY unit_settings
    ADD CONSTRAINT unit_settings_pkey PRIMARY KEY (unit_id);

ALTER TABLE ONLY unit_state
    ADD CONSTRAINT unit_state_pkey PRIMARY KEY (unit_id);

ALTER TABLE ONLY update_events
    ADD CONSTRAINT update_events_pkey PRIMARY KEY (stack_id, update_id, sequence);

ALTER TABLE ONLY update_journal
    ADD CONSTRAINT update_journal_pkey PRIMARY KEY (stack_id, update_id, arrival);

ALTER TABLE ONLY update_journal_refusals
    ADD CONSTRAINT update_journal_refusals_pkey PRIMARY KEY (stack_id, update_id, arrival);

ALTER TABLE ONLY update_journal_sequences
    ADD CONSTRAINT update_journal_sequences_pkey PRIMARY KEY (stack_id, update_id, sequence);

ALTER TABLE ONLY updates
    ADD CONSTRAINT updates_pkey PRIMARY KEY (stack_id, id);

ALTER TABLE ONLY used_tickets
    ADD CONSTRAINT used_tickets_pkey PRIMARY KEY (provider_id, ticket_digest);

ALTER TABLE ONLY workspace_blame_cursors
    ADD CONSTRAINT workspace_blame_cursors_pkey PRIMARY KEY (workspace_id);

ALTER TABLE ONLY workspace_blame_items
    ADD CONSTRAINT workspace_blame_items_pkey PRIMARY KEY (workspace_id, state_version_id);

ALTER TABLE ONLY workspace_blame_items
    ADD CONSTRAINT workspace_blame_items_workspace_id_ordinal_key UNIQUE (workspace_id, ordinal);

ALTER TABLE ONLY workspace_resource_changes
    ADD CONSTRAINT workspace_resource_changes_pkey PRIMARY KEY (workspace_id, state_version_id, address);

ALTER TABLE ONLY workspaces
    ADD CONSTRAINT workspaces_namespace_name_key UNIQUE (namespace, name);

ALTER TABLE ONLY workspaces
    ADD CONSTRAINT workspaces_pkey PRIMARY KEY (id);

CREATE INDEX access_tokens_created ON access_tokens USING btree (created_at_ms DESC, id COLLATE "C" DESC);

CREATE UNIQUE INDEX access_tokens_digest ON access_tokens USING btree (digest);

CREATE INDEX access_tokens_principal ON access_tokens USING btree (principal_id, created_at_ms DESC, id COLLATE "C" DESC);

CREATE INDEX audit_entries_ns_time ON audit_entries USING btree (namespace, at_ms DESC, sequence DESC);

CREATE INDEX blob_purges_age ON blob_purges USING btree (purged_at_ms, tool COLLATE "C", unit_id COLLATE "C");

CREATE INDEX break_glass_reviews_due ON break_glass_reviews USING btree (state, next_notice_at_ms);

CREATE UNIQUE INDEX break_glass_reviews_use ON break_glass_reviews USING btree (use_kind, use_ref);

CREATE INDEX collection_members_collection ON collection_members USING btree (namespace, collection_key);

CREATE UNIQUE INDEX drift_results_job ON drift_results USING btree (job_id);

CREATE INDEX drift_results_namespace ON drift_results USING btree (namespace_id);

CREATE INDEX durable_heartbeats_at ON durable_heartbeats USING btree (at_ms);

CREATE INDEX durable_operations_created ON durable_operations USING btree (created_at_ms, id);

CREATE INDEX durable_operations_open ON durable_operations USING btree (created_at_ms, id) WHERE (state = ANY (ARRAY['pending'::text, 'running'::text]));

CREATE INDEX forge_account_links_principal ON forge_account_links USING btree (principal_id);

CREATE INDEX forge_link_requests_principal ON forge_link_requests USING btree (principal_id, expires_at_ms);

CREATE INDEX gate_objects_kind_id ON gate_objects USING btree (kind, id);

CREATE INDEX gate_objects_namespace ON gate_objects USING btree (namespace_id, level);

CREATE INDEX gate_objects_unit ON gate_objects USING btree (namespace_id, unit_id);

CREATE INDEX gate_records_change ON gate_records USING btree (namespace_id, change_kind, change_id, at_ms, id);

CREATE INDEX gate_records_point ON gate_records USING btree (namespace_id, point, at_ms, id);

CREATE INDEX gate_records_refusal ON gate_records USING btree (namespace_id, refusal_key, last_at_ms) WHERE (refusal_key <> ''::text);

CREATE INDEX gate_references_owner ON gate_references USING btree (namespace_id, scope_key, owner_kind, owner_id);

CREATE UNIQUE INDEX git_host_connections_default ON git_host_connections USING btree (namespace_id) WHERE is_default;

CREATE UNIQUE INDEX git_host_connections_installation ON git_host_connections USING btree (namespace_id, kind, installation_id);

CREATE INDEX git_host_installations_namespace ON git_host_installations USING btree (namespace_id);

CREATE UNIQUE INDEX grants_current ON grants USING btree (subject, role, target) WHERE (revoked_at_ms IS NULL);

CREATE INDEX grants_holder ON grants USING btree (subject_kind, subject_id) WHERE (revoked_at_ms IS NULL);

CREATE INDEX grants_role ON grants USING btree (role) WHERE (revoked_at_ms IS NULL);

CREATE INDEX grants_target ON grants USING btree (target, subject, role) WHERE (revoked_at_ms IS NULL);

CREATE UNIQUE INDEX group_members_current ON group_members USING btree (group_id, principal_id) WHERE (removed_at_ms IS NULL);

CREATE INDEX group_members_principal ON group_members USING btree (principal_id) WHERE (removed_at_ms IS NULL);

CREATE INDEX idempotency_records_created ON idempotency_records USING btree (created_at_ms);

CREATE INDEX import_ledger_order ON import_ledger USING btree (namespace_id, import_id, import_unit_id, committed_at_ms, version);

CREATE INDEX imports_created ON imports USING btree (namespace_id, created_at_ms, id);

CREATE INDEX inventory_changes_unit ON inventory_changes USING btree (namespace_id, tool, unit_id, seq) INCLUDE (at_ms);

CREATE INDEX inventory_deleted_namespace ON inventory_deleted USING btree (namespace, tool, name);

CREATE INDEX inventory_deleted_purge_after ON inventory_deleted USING btree (purge_after_ms, tool COLLATE "C", unit_id COLLATE "C");

CREATE INDEX inventory_labels_namespace ON inventory_labels USING btree (namespace, unit_id);

CREATE INDEX inventory_labels_value ON inventory_labels USING btree (namespace, label_key, label_value, unit_id COLLATE "C");

CREATE INDEX inventory_namespace_unit_id ON inventory USING btree (namespace, unit_id COLLATE "C");

CREATE INDEX inventory_unit_id ON inventory USING btree (unit_id);

CREATE UNIQUE INDEX issuer_keys_one_active ON issuer_keys USING btree (namespace_id) WHERE (state = 'active'::text);

CREATE UNIQUE INDEX issuer_keys_one_next ON issuer_keys USING btree (namespace_id) WHERE (state = 'next'::text);

CREATE UNIQUE INDEX item_keys_item ON item_keys USING btree (namespace_id, scope_id, kind, item);

CREATE INDEX item_keys_scope ON item_keys USING btree (namespace_id, scope_id, id);

CREATE INDEX item_keys_wrapper ON item_keys USING btree (wrapper_id) WHERE (shredded_at_ms = 0);

CREATE INDEX job_freezes_namespace ON job_freezes USING btree (namespace_id, starts_at_ms);

CREATE INDEX job_logs_job ON job_logs USING btree (job_id);

CREATE INDEX job_plans_job ON job_plans USING btree (job_id);

CREATE UNIQUE INDEX jobs_key ON jobs USING btree (namespace_id, idempotency_key) WHERE (idempotency_key <> ''::text);

CREATE UNIQUE INDEX jobs_list ON jobs USING btree (namespace_id, seq);

CREATE UNIQUE INDEX jobs_one_change ON jobs USING btree (unit_id) WHERE (current_changes AND (state = ANY (ARRAY['dispatched'::text, 'running'::text, 'canceling'::text])));

CREATE INDEX jobs_open ON jobs USING btree (namespace_id, state) WHERE (state <> ALL (ARRAY['succeeded'::text, 'failed'::text, 'canceled'::text, 'rejected'::text, 'expired'::text]));

CREATE INDEX jobs_unit ON jobs USING btree (unit_id, seq);

CREATE INDEX local_user_resets_expiry ON local_user_resets USING btree (expires_at_ms);

CREATE INDEX manual_locks_namespace ON manual_locks USING btree (namespace);

CREATE INDEX member_removals_source ON member_removals USING btree (source_kind, source_id, at_ms);

CREATE UNIQUE INDEX namespaces_id ON namespaces USING btree (id);

CREATE INDEX notification_deliveries_due ON notification_deliveries USING btree (scope, next_at_ms, id) WHERE (state = 'pending'::text);

CREATE INDEX notification_deliveries_subscription ON notification_deliveries USING btree (scope, subscription_id, next_at_ms DESC, id DESC);

CREATE INDEX notification_sink_secrets_sink ON notification_sink_secrets USING btree (scope, sink_id, "position");

CREATE INDEX notification_sinks_scope ON notification_sinks USING btree (scope, id);

CREATE INDEX notification_subscriptions_scope ON notification_subscriptions USING btree (scope, id);

CREATE INDEX notification_subscriptions_sink ON notification_subscriptions USING btree (scope, sink_id);

CREATE INDEX object_context_owners_entry ON object_context_owners USING btree (namespace_id, object_kind, object_id, resource_key, source);

CREATE INDEX passkey_challenges_expiry ON passkey_challenges USING btree (expires_at_ms);

CREATE INDEX passkey_challenges_source ON passkey_challenges USING btree (source, expires_at_ms);

CREATE INDEX passkey_challenges_source_group ON passkey_challenges USING btree (source_group, expires_at_ms);

CREATE INDEX passkeys_principal ON passkeys USING btree (principal_id, created_at_ms, id COLLATE "C");

CREATE INDEX placement_rules_order ON placement_rules USING btree (namespace, "position");

CREATE UNIQUE INDEX principal_groups_name_key ON principal_groups USING btree (name_key COLLATE "C") WHERE (deleted_at_ms IS NULL);

CREATE INDEX principals_namespace ON principals USING btree (namespace, subject COLLATE "C") WHERE (deleted_at_ms IS NULL);

CREATE UNIQUE INDEX principals_subject ON principals USING btree (subject COLLATE "C") WHERE (deleted_at_ms IS NULL);

CREATE UNIQUE INDEX profiles_live_name ON profiles USING btree (namespace_id, name) WHERE (deleted_at_ms = 0);

CREATE UNIQUE INDEX roles_name_key ON roles USING btree (name_key COLLATE "C") WHERE (deleted_at_ms IS NULL);

CREATE INDEX runner_join_tokens_pool ON runner_join_tokens USING btree (pool_id, created_at_ms, id);

CREATE INDEX runner_nonces_expiry ON runner_nonces USING btree (expires_at_ms);

CREATE UNIQUE INDEX runners_name ON runners USING btree (pool_id, name) WHERE (retired_at_ms = 0);

CREATE INDEX saved_views_owner ON saved_views USING btree (owner);

CREATE UNIQUE INDEX saved_views_owner_name ON saved_views USING btree (namespace_id, owner, name);

CREATE UNIQUE INDEX self_service_blueprint_versions_draft ON self_service_blueprint_versions USING btree (namespace_id, name, draft_version);

CREATE UNIQUE INDEX sessions_digest ON sessions USING btree (digest);

CREATE INDEX sessions_principal ON sessions USING btree (principal_id, created_at_ms DESC, id COLLATE "C" DESC);

CREATE INDEX sign_in_failures_window_end ON sign_in_failures USING btree (window_end_ms);

CREATE INDEX sign_in_requests_expiry ON sign_in_requests USING btree (expires_at_ms);

CREATE UNIQUE INDEX stacks_id ON stacks USING btree (id);

CREATE INDEX state_versions_workspace_finalized ON state_versions USING btree (workspace_id, finalized_at_ms DESC);

CREATE INDEX state_versions_workspace_id ON state_versions USING btree (workspace_id);

CREATE INDEX triggers_source ON triggers USING btree (namespace_id, source_tool, source_unit_id) WHERE ((kind = 'follows'::text) AND (enabled = 1));

CREATE INDEX triggers_unit ON triggers USING btree (namespace_id, unit_tool, unit_id);

CREATE UNIQUE INDEX trust_rules_name ON trust_rules USING btree (namespace, name);

CREATE INDEX trust_rules_principal ON trust_rules USING btree (principal_id);

CREATE INDEX unit_settings_namespace ON unit_settings USING btree (namespace_id);

CREATE INDEX updates_history ON updates USING btree (stack_id, version) WHERE ((status <> 'not started'::text) AND (NOT dry_run) AND (kind <> 'preview'::text));

CREATE UNIQUE INDEX updates_one_running ON updates USING btree (stack_id) WHERE ((status = 'running'::text) AND (NOT dry_run) AND (kind <> 'preview'::text));

CREATE INDEX used_tickets_until ON used_tickets USING btree (until_ms);

CREATE INDEX workspace_resource_changes_address ON workspace_resource_changes USING btree (workspace_id, address, ordinal DESC);

CREATE INDEX workspaces_live_name ON workspaces USING btree (namespace, name) WHERE (deleted_at_ms IS NULL);

ALTER TABLE ONLY access_tokens
    ADD CONSTRAINT access_tokens_principal_id_fkey FOREIGN KEY (principal_id) REFERENCES principals(id);

ALTER TABLE ONLY checkpoints
    ADD CONSTRAINT checkpoints_stack_id_fkey FOREIGN KEY (stack_id) REFERENCES stacks(id) ON DELETE CASCADE;

ALTER TABLE ONLY checkpoints
    ADD CONSTRAINT checkpoints_update_fkey FOREIGN KEY (stack_id, update_id) REFERENCES updates(stack_id, id) ON DELETE CASCADE;

ALTER TABLE ONLY collection_members
    ADD CONSTRAINT collection_members_namespace_collection_key_fkey FOREIGN KEY (namespace, collection_key) REFERENCES collections(namespace, name_key);

ALTER TABLE ONLY collections
    ADD CONSTRAINT collections_namespace_fkey FOREIGN KEY (namespace) REFERENCES namespaces(name);

ALTER TABLE ONLY drift_results
    ADD CONSTRAINT drift_results_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY durable_steps
    ADD CONSTRAINT durable_steps_operation_id_fkey FOREIGN KEY (operation_id) REFERENCES durable_operations(id) ON DELETE CASCADE;

ALTER TABLE ONLY forge_account_links
    ADD CONSTRAINT forge_account_links_principal_id_fkey FOREIGN KEY (principal_id) REFERENCES principals(id);

ALTER TABLE ONLY forge_link_requests
    ADD CONSTRAINT forge_link_requests_principal_id_fkey FOREIGN KEY (principal_id) REFERENCES principals(id);

ALTER TABLE ONLY gate_references
    ADD CONSTRAINT gate_references_namespace_id_scope_key_owner_kind_owner_id_fkey FOREIGN KEY (namespace_id, scope_key, owner_kind, owner_id) REFERENCES gate_objects(namespace_id, scope_key, kind, id) ON DELETE CASCADE;

ALTER TABLE ONLY git_host_connections
    ADD CONSTRAINT git_host_connections_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY git_host_installations
    ADD CONSTRAINT git_host_installations_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY group_mappings
    ADD CONSTRAINT group_mappings_group_id_fkey FOREIGN KEY (group_id) REFERENCES principal_groups(id);

ALTER TABLE ONLY group_members
    ADD CONSTRAINT group_members_group_id_fkey FOREIGN KEY (group_id) REFERENCES principal_groups(id);

ALTER TABLE ONLY group_members
    ADD CONSTRAINT group_members_principal_id_fkey FOREIGN KEY (principal_id) REFERENCES principals(id);

ALTER TABLE ONLY import_units
    ADD CONSTRAINT import_units_namespace_id_import_id_fkey FOREIGN KEY (namespace_id, import_id) REFERENCES imports(namespace_id, id);

ALTER TABLE ONLY inventory_deleted
    ADD CONSTRAINT inventory_deleted_namespace_fkey FOREIGN KEY (namespace) REFERENCES namespaces(name);

ALTER TABLE ONLY inventory_labels
    ADD CONSTRAINT inventory_labels_namespace_fkey FOREIGN KEY (namespace) REFERENCES namespaces(name);

ALTER TABLE ONLY inventory
    ADD CONSTRAINT inventory_namespace_fkey FOREIGN KEY (namespace) REFERENCES namespaces(name);

ALTER TABLE ONLY item_keys
    ADD CONSTRAINT item_keys_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY job_freezes
    ADD CONSTRAINT job_freezes_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY job_log_chunks
    ADD CONSTRAINT job_log_chunks_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY job_logs
    ADD CONSTRAINT job_logs_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY job_plans
    ADD CONSTRAINT job_plans_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY jobs
    ADD CONSTRAINT jobs_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY local_user_resets
    ADD CONSTRAINT local_user_resets_user_id_principal_id_fkey FOREIGN KEY (user_id, principal_id) REFERENCES local_users(id, principal_id) ON DELETE CASCADE;

ALTER TABLE ONLY local_users
    ADD CONSTRAINT local_users_principal_id_fkey FOREIGN KEY (principal_id) REFERENCES principals(id);

ALTER TABLE ONLY manual_locks
    ADD CONSTRAINT manual_locks_namespace_fkey FOREIGN KEY (namespace) REFERENCES namespaces(name);

ALTER TABLE ONLY object_context
    ADD CONSTRAINT object_context_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY object_context_owners
    ADD CONSTRAINT object_context_owners_namespace_id_object_kind_object_id_r_fkey FOREIGN KEY (namespace_id, object_kind, object_id, resource_key, source) REFERENCES object_context(namespace_id, object_kind, object_id, resource_key, source) ON DELETE CASCADE;

ALTER TABLE ONLY passkeys
    ADD CONSTRAINT passkeys_principal_id_fkey FOREIGN KEY (principal_id) REFERENCES principals(id);

ALTER TABLE ONLY person_preferences
    ADD CONSTRAINT person_preferences_principal_id_fkey FOREIGN KEY (principal_id) REFERENCES principals(id);

ALTER TABLE ONLY placement_rules
    ADD CONSTRAINT placement_rules_namespace_collection_key_fkey FOREIGN KEY (namespace, collection_key) REFERENCES collections(namespace, name_key);

ALTER TABLE ONLY placement_rules
    ADD CONSTRAINT placement_rules_namespace_fkey FOREIGN KEY (namespace) REFERENCES namespaces(name);

ALTER TABLE ONLY principal_session_generations
    ADD CONSTRAINT principal_session_generations_principal_id_fkey FOREIGN KEY (principal_id) REFERENCES principals(id);

ALTER TABLE ONLY profile_revisions
    ADD CONSTRAINT profile_revisions_namespace_id_profile_id_fkey FOREIGN KEY (namespace_id, profile_id) REFERENCES profiles(namespace_id, id);

ALTER TABLE ONLY registry_keys
    ADD CONSTRAINT registry_keys_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY registry_releases
    ADD CONSTRAINT registry_releases_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY runner_join_tokens
    ADD CONSTRAINT runner_join_tokens_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY runner_join_tokens
    ADD CONSTRAINT runner_join_tokens_pool_id_fkey FOREIGN KEY (pool_id) REFERENCES runner_pools(id);

ALTER TABLE ONLY runner_pools
    ADD CONSTRAINT runner_pools_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY runners
    ADD CONSTRAINT runners_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY runners
    ADD CONSTRAINT runners_pool_id_fkey FOREIGN KEY (pool_id) REFERENCES runner_pools(id);

ALTER TABLE ONLY saved_views
    ADD CONSTRAINT saved_views_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY secret_keys
    ADD CONSTRAINT secret_keys_namespace_fkey FOREIGN KEY (namespace) REFERENCES namespaces(name);

ALTER TABLE ONLY self_service_blueprint_versions
    ADD CONSTRAINT self_service_blueprint_versions_namespace_id_name_fkey FOREIGN KEY (namespace_id, name) REFERENCES self_service_blueprints(namespace_id, name);

ALTER TABLE ONLY self_service_blueprints
    ADD CONSTRAINT self_service_blueprints_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY sessions
    ADD CONSTRAINT sessions_principal_id_fkey FOREIGN KEY (principal_id) REFERENCES principals(id);

ALTER TABLE ONLY stack_tags
    ADD CONSTRAINT stack_tags_stack_id_fkey FOREIGN KEY (stack_id) REFERENCES stacks(id) ON DELETE CASCADE;

ALTER TABLE ONLY stacks
    ADD CONSTRAINT stacks_namespace_fkey FOREIGN KEY (namespace) REFERENCES namespaces(name);

ALTER TABLE ONLY state_versions
    ADD CONSTRAINT state_versions_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id);

ALTER TABLE ONLY triggers
    ADD CONSTRAINT triggers_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY trust_rules
    ADD CONSTRAINT trust_rules_principal_id_fkey FOREIGN KEY (principal_id) REFERENCES principals(id);

ALTER TABLE ONLY unit_defaults
    ADD CONSTRAINT unit_defaults_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY unit_settings
    ADD CONSTRAINT unit_settings_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY unit_state
    ADD CONSTRAINT unit_state_namespace_id_fkey FOREIGN KEY (namespace_id) REFERENCES namespaces(id);

ALTER TABLE ONLY update_events
    ADD CONSTRAINT update_events_stack_id_fkey FOREIGN KEY (stack_id) REFERENCES stacks(id) ON DELETE CASCADE;

ALTER TABLE ONLY update_events
    ADD CONSTRAINT update_events_update_fkey FOREIGN KEY (stack_id, update_id) REFERENCES updates(stack_id, id) ON DELETE CASCADE;

ALTER TABLE ONLY update_journal_refusals
    ADD CONSTRAINT update_journal_refusals_stack_id_update_id_fkey FOREIGN KEY (stack_id, update_id) REFERENCES updates(stack_id, id) ON DELETE CASCADE;

ALTER TABLE ONLY update_journal_sequences
    ADD CONSTRAINT update_journal_sequences_stack_id_update_id_arrival_fkey FOREIGN KEY (stack_id, update_id, arrival) REFERENCES update_journal(stack_id, update_id, arrival) ON DELETE CASCADE;

ALTER TABLE ONLY update_journal
    ADD CONSTRAINT update_journal_stack_id_update_id_fkey FOREIGN KEY (stack_id, update_id) REFERENCES updates(stack_id, id) ON DELETE CASCADE;

ALTER TABLE ONLY updates
    ADD CONSTRAINT updates_stack_id_fkey FOREIGN KEY (stack_id) REFERENCES stacks(id) ON DELETE CASCADE;

ALTER TABLE ONLY workspace_blame_cursors
    ADD CONSTRAINT workspace_blame_cursors_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id);

ALTER TABLE ONLY workspace_blame_items
    ADD CONSTRAINT workspace_blame_items_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id);

ALTER TABLE ONLY workspace_resource_changes
    ADD CONSTRAINT workspace_resource_changes_workspace_id_state_version_id_fkey FOREIGN KEY (workspace_id, state_version_id) REFERENCES workspace_blame_items(workspace_id, state_version_id);

ALTER TABLE ONLY workspaces
    ADD CONSTRAINT workspaces_namespace_fkey FOREIGN KEY (namespace) REFERENCES namespaces(name);

CREATE TABLE backups (
    id text NOT NULL,
    state text NOT NULL,
    started_at_ms bigint NOT NULL,
    ended_at_ms bigint,
    target text NOT NULL,
    error text DEFAULT ''::text NOT NULL
);

CREATE TABLE installation (
    id bigint NOT NULL,
    state text DEFAULT 'normal'::text NOT NULL,
    kind text,
    CONSTRAINT installation_id_check CHECK ((id = 1)),
    CONSTRAINT installation_kind_check CHECK (((kind IS NULL) OR (kind = ANY (ARRAY['dev'::text, 'production'::text])))),
    CONSTRAINT installation_state_check CHECK ((state = ANY (ARRAY['normal'::text, 'maintenance'::text])))
);

CREATE TABLE instance_versions (
    id text NOT NULL,
    binary_version text NOT NULL,
    started_at_ms bigint NOT NULL,
    CONSTRAINT instance_versions_binary_version CHECK (((length(binary_version) >= 1) AND (length(binary_version) <= 64))),
    CONSTRAINT instance_versions_id CHECK (((length(id) >= 1) AND (length(id) <= 64)))
);

CREATE TABLE maintenance_revocations (
    id bigint NOT NULL,
    kind text NOT NULL,
    target text NOT NULL,
    end_reason text NOT NULL,
    actor text NOT NULL,
    object text NOT NULL,
    at_ms bigint NOT NULL,
    CONSTRAINT maintenance_revocations_kind CHECK ((kind = ANY (ARRAY['session'::text, 'token'::text, 'principal'::text, 'temp-admin'::text, 'grant'::text, 'join-token'::text, 'provider'::text, 'runner'::text])))
);

CREATE SEQUENCE maintenance_revocations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE maintenance_revocations_id_seq OWNED BY maintenance_revocations.id;

CREATE TABLE restores (
    id text NOT NULL,
    state text NOT NULL,
    backup_id text NOT NULL,
    pre_backup_id text DEFAULT ''::text NOT NULL,
    requested_by text DEFAULT ''::text NOT NULL,
    started_at_ms bigint NOT NULL,
    ended_at_ms bigint,
    error text DEFAULT ''::text NOT NULL,
    dev_report text DEFAULT ''::text NOT NULL,
    maintenance_entered boolean DEFAULT false NOT NULL,
    audit_events text DEFAULT '[]'::text NOT NULL
);

CREATE TABLE store_checks (
    id text NOT NULL,
    target text NOT NULL,
    state text NOT NULL,
    requested_by text NOT NULL,
    started_at_ms bigint NOT NULL,
    ended_at_ms bigint,
    error text DEFAULT ''::text NOT NULL,
    report text DEFAULT ''::text NOT NULL,
    CONSTRAINT store_checks_id CHECK (((length(id) >= 5) AND (length(id) <= 40))),
    CONSTRAINT store_checks_state CHECK ((state = ANY (ARRAY['running'::text, 'succeeded'::text, 'failed'::text]))),
    CONSTRAINT store_checks_target CHECK ((target = ANY (ARRAY['blobs'::text, 'backup'::text])))
);

ALTER TABLE ONLY maintenance_revocations ALTER COLUMN id SET DEFAULT nextval('maintenance_revocations_id_seq'::regclass);

INSERT INTO installation (id, state, kind) VALUES (1, 'normal', NULL);

ALTER TABLE ONLY backups
    ADD CONSTRAINT backups_pkey PRIMARY KEY (id);

ALTER TABLE ONLY installation
    ADD CONSTRAINT installation_pkey PRIMARY KEY (id);

ALTER TABLE ONLY instance_versions
    ADD CONSTRAINT instance_versions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY maintenance_revocations
    ADD CONSTRAINT maintenance_revocations_pkey PRIMARY KEY (id);

ALTER TABLE ONLY restores
    ADD CONSTRAINT restores_pkey PRIMARY KEY (id);

ALTER TABLE ONLY store_checks
    ADD CONSTRAINT store_checks_pkey PRIMARY KEY (id);

CREATE UNIQUE INDEX backups_one_running ON backups USING btree (((state = 'running'::text))) WHERE (state = 'running'::text);

CREATE INDEX backups_started_at_ms ON backups USING btree (started_at_ms DESC);

CREATE UNIQUE INDEX restores_one_running ON restores USING btree (((state = ANY (ARRAY['checking'::text, 'applying'::text])))) WHERE (state = ANY (ARRAY['checking'::text, 'applying'::text]));

CREATE INDEX restores_started_at_ms ON restores USING btree (started_at_ms DESC);

CREATE UNIQUE INDEX store_checks_one_running ON store_checks USING btree (((state = 'running'::text))) WHERE (state = 'running'::text);

CREATE INDEX store_checks_started_at_ms ON store_checks USING btree (started_at_ms DESC);

CREATE INDEX store_checks_target_ended ON store_checks USING btree (target, ended_at_ms);

DO $$
DECLARE
  live text := current_schema();
  ctl text := current_schema() || '_control';
BEGIN
  EXECUTE format('CREATE SCHEMA IF NOT EXISTS %I', ctl);
  EXECUTE format('ALTER TABLE %I.backups SET SCHEMA %I', live, ctl);
  EXECUTE format('ALTER TABLE %I.installation SET SCHEMA %I', live, ctl);
  EXECUTE format('ALTER TABLE %I.instance_versions SET SCHEMA %I', live, ctl);
  EXECUTE format('ALTER TABLE %I.maintenance_revocations SET SCHEMA %I', live, ctl);
  EXECUTE format('ALTER TABLE %I.restores SET SCHEMA %I', live, ctl);
  EXECUTE format('ALTER TABLE %I.store_checks SET SCHEMA %I', live, ctl);
END $$;
