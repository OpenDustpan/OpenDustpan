-- minimum-binary-version: v0.1.0
-- Generated file. Do not edit it by hand. make regenerate-baseline writes it
-- from the postgres migrations of origin/main (6be2f2a0661a).
-- This baseline replaced every migration before it, as a one-time exception
-- to the rule that an applied migration never changes. No binary opens a
-- database or restores a backup from before it.
-- pg_dump major version: 18

CREATE TABLE access_token_policy (
    id bigint NOT NULL,
    max_lifetime_ms bigint NOT NULL,
    inactivity_ms bigint NOT NULL,
    disable_tokens boolean NOT NULL,
    disable_accounts boolean NOT NULL,
    disable_break_glass boolean NOT NULL,
    updated_at_ms bigint NOT NULL,
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
    fence_version bigint DEFAULT 0 NOT NULL
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

CREATE TABLE backups (
    id text NOT NULL,
    state text NOT NULL,
    started_at_ms bigint NOT NULL,
    ended_at_ms bigint,
    target text NOT NULL,
    error text DEFAULT ''::text NOT NULL
);

CREATE TABLE blob_purges (
    tool text NOT NULL,
    unit_id text NOT NULL,
    namespace text NOT NULL,
    purged_at_ms bigint NOT NULL
);

CREATE TABLE checkpoints (
    update_id text NOT NULL,
    lease_digest bytea NOT NULL,
    schema_version bigint NOT NULL,
    sequence bigint NOT NULL,
    digest bytea NOT NULL,
    invalid boolean NOT NULL,
    features jsonb NOT NULL,
    deployment bytea NOT NULL,
    saved_at_ms bigint NOT NULL,
    stack_id text NOT NULL,
    blob_key text,
    size bigint,
    blob_size bigint,
    blob_md5 bytea
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

CREATE TABLE installation (
    id bigint NOT NULL,
    state text DEFAULT 'normal'::text NOT NULL,
    CONSTRAINT installation_id_check CHECK ((id = 1)),
    CONSTRAINT installation_state_check CHECK ((state = ANY (ARRAY['normal'::text, 'maintenance'::text])))
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
    lock_operation text
);

CREATE TABLE inventory_deleted (
    namespace text NOT NULL,
    tool text NOT NULL,
    unit_id text NOT NULL,
    name text NOT NULL,
    deleted_at_ms bigint NOT NULL,
    purge_after_ms bigint NOT NULL
);

CREATE TABLE namespaces (
    name text NOT NULL,
    home_region text NOT NULL,
    failover_version bigint NOT NULL,
    created_at_ms bigint NOT NULL,
    allow_forced_writes boolean DEFAULT false NOT NULL,
    allow_unlocked_writes boolean DEFAULT false NOT NULL,
    ip_allowlist text DEFAULT ''::text NOT NULL
);

CREATE TABLE oidc_providers (
    provider_id text NOT NULL,
    issuer text NOT NULL,
    created_at_ms bigint NOT NULL
);

CREATE TABLE principal_groups (
    id text NOT NULL,
    name text NOT NULL,
    name_key text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    source text DEFAULT 'local'::text NOT NULL,
    created_at_ms bigint NOT NULL,
    updated_at_ms bigint NOT NULL,
    deleted_at_ms bigint
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

CREATE TABLE restores (
    id text NOT NULL,
    state text NOT NULL,
    backup_id text NOT NULL,
    pre_backup_id text DEFAULT ''::text NOT NULL,
    requested_by text DEFAULT ''::text NOT NULL,
    started_at_ms bigint NOT NULL,
    ended_at_ms bigint,
    error text DEFAULT ''::text NOT NULL
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

CREATE TABLE secret_keys (
    namespace text NOT NULL,
    stack_id text NOT NULL,
    wrapper_id text NOT NULL,
    ciphertext bytea NOT NULL,
    created_at_ms bigint NOT NULL
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
    user_agent text DEFAULT ''::text NOT NULL
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
    version bigint NOT NULL
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
    unlocked_write boolean DEFAULT false NOT NULL
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
    journal_records_changed boolean DEFAULT false NOT NULL
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
    deleted_name text
);

INSERT INTO access_token_policy (id, max_lifetime_ms, inactivity_ms, disable_tokens, disable_accounts, disable_break_glass, updated_at_ms) VALUES (1, 31536000000, 7776000000, true, true, false, 0);

INSERT INTO audit_chain_state (id, state, changed_at_ms, changed_by, pending, entered_maintenance, log_replaced, migrating_at_ms) VALUES (1, 'on', 0, '', false, false, false, 0);

INSERT INTO installation (id, state) VALUES (1, 'normal');

INSERT INTO session_policy (id, idle_ms, max_length_ms, updated_at_ms, token_freshness_ms) VALUES (1, 900000, 43200000, 0, NULL);

ALTER TABLE ONLY access_token_policy
    ADD CONSTRAINT access_token_policy_pkey PRIMARY KEY (id);

ALTER TABLE ONLY access_tokens
    ADD CONSTRAINT access_tokens_pkey PRIMARY KEY (id);

ALTER TABLE ONLY audit_chain_state
    ADD CONSTRAINT audit_chain_state_pkey PRIMARY KEY (id);

ALTER TABLE ONLY audit_entries
    ADD CONSTRAINT audit_entries_pkey PRIMARY KEY (namespace, sequence);

ALTER TABLE ONLY audit_sink_cursors
    ADD CONSTRAINT audit_sink_cursors_pkey PRIMARY KEY (sink, namespace);

ALTER TABLE ONLY audit_sink_leases
    ADD CONSTRAINT audit_sink_leases_pkey PRIMARY KEY (sink);

ALTER TABLE ONLY backups
    ADD CONSTRAINT backups_pkey PRIMARY KEY (id);

ALTER TABLE ONLY blob_purges
    ADD CONSTRAINT blob_purges_pkey PRIMARY KEY (tool, unit_id);

ALTER TABLE ONLY checkpoints
    ADD CONSTRAINT checkpoints_pkey PRIMARY KEY (stack_id, update_id);

ALTER TABLE ONLY collection_members
    ADD CONSTRAINT collection_members_pkey PRIMARY KEY (unit_id);

ALTER TABLE ONLY collections
    ADD CONSTRAINT collections_id_key UNIQUE (id);

ALTER TABLE ONLY collections
    ADD CONSTRAINT collections_pkey PRIMARY KEY (namespace, name_key);

ALTER TABLE ONLY grants
    ADD CONSTRAINT grants_pkey PRIMARY KEY (id);

ALTER TABLE ONLY group_members
    ADD CONSTRAINT group_members_pkey PRIMARY KEY (group_id, principal_id, added_at_ms);

ALTER TABLE ONLY installation
    ADD CONSTRAINT installation_pkey PRIMARY KEY (id);

ALTER TABLE ONLY installation_secret_keys
    ADD CONSTRAINT installation_secret_keys_pkey PRIMARY KEY (scope_id);

ALTER TABLE ONLY inventory_deleted
    ADD CONSTRAINT inventory_deleted_pkey PRIMARY KEY (tool, unit_id);

ALTER TABLE ONLY inventory
    ADD CONSTRAINT inventory_pkey PRIMARY KEY (namespace, tool, name);

ALTER TABLE ONLY namespaces
    ADD CONSTRAINT namespaces_pkey PRIMARY KEY (name);

ALTER TABLE ONLY oidc_providers
    ADD CONSTRAINT oidc_providers_pkey PRIMARY KEY (provider_id);

ALTER TABLE ONLY principal_groups
    ADD CONSTRAINT principal_groups_pkey PRIMARY KEY (id);

ALTER TABLE ONLY principals
    ADD CONSTRAINT principals_pkey PRIMARY KEY (id);

ALTER TABLE ONLY restores
    ADD CONSTRAINT restores_pkey PRIMARY KEY (id);

ALTER TABLE ONLY roles
    ADD CONSTRAINT roles_pkey PRIMARY KEY (id);

ALTER TABLE ONLY secret_keys
    ADD CONSTRAINT secret_keys_pkey PRIMARY KEY (namespace, stack_id);

ALTER TABLE ONLY session_policy
    ADD CONSTRAINT session_policy_pkey PRIMARY KEY (id);

ALTER TABLE ONLY sessions
    ADD CONSTRAINT sessions_pkey PRIMARY KEY (id);

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

ALTER TABLE ONLY workspaces
    ADD CONSTRAINT workspaces_namespace_name_key UNIQUE (namespace, name);

ALTER TABLE ONLY workspaces
    ADD CONSTRAINT workspaces_pkey PRIMARY KEY (id);

CREATE INDEX access_tokens_created ON access_tokens USING btree (created_at_ms DESC, id COLLATE "C" DESC);

CREATE UNIQUE INDEX access_tokens_digest ON access_tokens USING btree (digest);

CREATE INDEX access_tokens_principal ON access_tokens USING btree (principal_id, created_at_ms DESC, id COLLATE "C" DESC);

CREATE INDEX audit_entries_ns_time ON audit_entries USING btree (namespace, at_ms DESC, sequence DESC);

CREATE UNIQUE INDEX backups_one_running ON backups USING btree (((state = 'running'::text))) WHERE (state = 'running'::text);

CREATE INDEX backups_started_at_ms ON backups USING btree (started_at_ms DESC);

CREATE INDEX blob_purges_age ON blob_purges USING btree (purged_at_ms, tool COLLATE "C", unit_id COLLATE "C");

CREATE INDEX collection_members_collection ON collection_members USING btree (namespace, collection_key);

CREATE UNIQUE INDEX grants_current ON grants USING btree (subject, role, target) WHERE (revoked_at_ms IS NULL);

CREATE INDEX grants_holder ON grants USING btree (subject_kind, subject_id) WHERE (revoked_at_ms IS NULL);

CREATE INDEX grants_role ON grants USING btree (role) WHERE (revoked_at_ms IS NULL);

CREATE INDEX grants_target ON grants USING btree (target, subject, role) WHERE (revoked_at_ms IS NULL);

CREATE UNIQUE INDEX group_members_current ON group_members USING btree (group_id, principal_id) WHERE (removed_at_ms IS NULL);

CREATE INDEX group_members_principal ON group_members USING btree (principal_id) WHERE (removed_at_ms IS NULL);

CREATE INDEX inventory_deleted_namespace ON inventory_deleted USING btree (namespace, tool, name);

CREATE INDEX inventory_deleted_purge_after ON inventory_deleted USING btree (purge_after_ms, tool COLLATE "C", unit_id COLLATE "C");

CREATE INDEX inventory_unit_id ON inventory USING btree (unit_id);

CREATE UNIQUE INDEX principal_groups_name_key ON principal_groups USING btree (name_key COLLATE "C") WHERE (deleted_at_ms IS NULL);

CREATE INDEX principals_namespace ON principals USING btree (namespace, subject COLLATE "C") WHERE (deleted_at_ms IS NULL);

CREATE UNIQUE INDEX principals_subject ON principals USING btree (subject COLLATE "C") WHERE (deleted_at_ms IS NULL);

CREATE UNIQUE INDEX restores_one_running ON restores USING btree (((state = ANY (ARRAY['checking'::text, 'applying'::text])))) WHERE (state = ANY (ARRAY['checking'::text, 'applying'::text]));

CREATE INDEX restores_started_at_ms ON restores USING btree (started_at_ms DESC);

CREATE UNIQUE INDEX roles_name_key ON roles USING btree (name_key COLLATE "C") WHERE (deleted_at_ms IS NULL);

CREATE UNIQUE INDEX sessions_digest ON sessions USING btree (digest);

CREATE INDEX sessions_principal ON sessions USING btree (principal_id, created_at_ms DESC, id COLLATE "C" DESC);

CREATE INDEX sign_in_requests_expiry ON sign_in_requests USING btree (expires_at_ms);

CREATE UNIQUE INDEX stacks_id ON stacks USING btree (id);

CREATE INDEX state_versions_workspace_finalized ON state_versions USING btree (workspace_id, finalized_at_ms DESC);

CREATE INDEX state_versions_workspace_id ON state_versions USING btree (workspace_id);

CREATE INDEX updates_history ON updates USING btree (stack_id, version) WHERE ((status <> 'not started'::text) AND (NOT dry_run) AND (kind <> 'preview'::text));

CREATE UNIQUE INDEX updates_one_running ON updates USING btree (stack_id) WHERE ((status = 'running'::text) AND (NOT dry_run) AND (kind <> 'preview'::text));

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

ALTER TABLE ONLY group_members
    ADD CONSTRAINT group_members_group_id_fkey FOREIGN KEY (group_id) REFERENCES principal_groups(id);

ALTER TABLE ONLY group_members
    ADD CONSTRAINT group_members_principal_id_fkey FOREIGN KEY (principal_id) REFERENCES principals(id);

ALTER TABLE ONLY inventory_deleted
    ADD CONSTRAINT inventory_deleted_namespace_fkey FOREIGN KEY (namespace) REFERENCES namespaces(name);

ALTER TABLE ONLY inventory
    ADD CONSTRAINT inventory_namespace_fkey FOREIGN KEY (namespace) REFERENCES namespaces(name);

ALTER TABLE ONLY secret_keys
    ADD CONSTRAINT secret_keys_namespace_fkey FOREIGN KEY (namespace) REFERENCES namespaces(name);

ALTER TABLE ONLY sessions
    ADD CONSTRAINT sessions_principal_id_fkey FOREIGN KEY (principal_id) REFERENCES principals(id);

ALTER TABLE ONLY stack_tags
    ADD CONSTRAINT stack_tags_stack_id_fkey FOREIGN KEY (stack_id) REFERENCES stacks(id) ON DELETE CASCADE;

ALTER TABLE ONLY stacks
    ADD CONSTRAINT stacks_namespace_fkey FOREIGN KEY (namespace) REFERENCES namespaces(name);

ALTER TABLE ONLY state_versions
    ADD CONSTRAINT state_versions_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id);

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

ALTER TABLE ONLY workspaces
    ADD CONSTRAINT workspaces_namespace_fkey FOREIGN KEY (namespace) REFERENCES namespaces(name);
