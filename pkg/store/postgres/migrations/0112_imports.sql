-- Imports: the move of units with their state and history from a source into
-- a namespace. All rows hold the stable ID of the namespace, never its name,
-- and the ledger has no foreign key, so the purge of an import record keeps
-- every ledger row. A purged import keeps its ID in imports_purged.
-- ended_at_ms is 0 for an import that did not end.
CREATE TABLE imports (
    namespace_id text NOT NULL,
    id           text NOT NULL,
    kind         text NOT NULL,
    address      text NOT NULL,
    selection    text NOT NULL,
    credential   text NOT NULL,
    profile      text NOT NULL,
    profile_path text NOT NULL,
    options      text NOT NULL,
    state        text NOT NULL,
    reason       text NOT NULL,
    created_by   text NOT NULL,
    created_at_ms bigint NOT NULL,
    plan         text NOT NULL,
    ended_at_ms bigint NOT NULL,
    PRIMARY KEY (namespace_id, id),
    CHECK (state IN ('planning', 'planned', 'running', 'paused', 'holding', 'released', 'failed', 'canceled')),
    CHECK (credential IN ('profile', 'ephemeral', 'none'))
);

CREATE INDEX imports_created ON imports (namespace_id, created_at_ms, id);

CREATE TABLE imports_purged (
    namespace_id text NOT NULL,
    id           text NOT NULL,
    purged_at_ms bigint NOT NULL,
    PRIMARY KEY (namespace_id, id)
);

-- One row for each unit that an import moves. position keeps the order of
-- the plan. The target is the unit that the import created, empty until then.
CREATE TABLE import_units (
    namespace_id   text NOT NULL,
    import_id      text NOT NULL,
    id             text NOT NULL,
    position       bigint NOT NULL,
    source_tool    text NOT NULL,
    source_name    text NOT NULL,
    target_name    text NOT NULL,
    target_tool    text NOT NULL,
    target_unit_id text NOT NULL,
    state          text NOT NULL,
    error_code     text NOT NULL,
    versions       bigint NOT NULL,
    committed      bigint NOT NULL,
    bytes          bigint NOT NULL,
    encryption     text NOT NULL,
    findings       text NOT NULL,
    PRIMARY KEY (namespace_id, import_id, id),
    FOREIGN KEY (namespace_id, import_id) REFERENCES imports (namespace_id, id),
    CHECK (state IN ('planned', 'skipped', 'running', 'paused', 'imported', 'failed', 'canceled'))
);

-- One row for each source version that an import unit committed. A row has
-- no foreign key to the import or to the history item: it stays after the
-- purge of the import record.
CREATE TABLE import_ledger (
    namespace_id   text NOT NULL,
    import_id      text NOT NULL,
    import_unit_id text NOT NULL,
    version        text NOT NULL,
    source_sha256  text NOT NULL,
    stored_sha256  text NOT NULL,
    item_tool      text NOT NULL,
    item_unit_id   text NOT NULL,
    item_kind      text NOT NULL,
    item_id        text NOT NULL,
    committed_at_ms bigint NOT NULL,
    PRIMARY KEY (namespace_id, import_id, import_unit_id, version)
);

CREATE INDEX import_ledger_order ON import_ledger (namespace_id, import_id, import_unit_id, committed_at_ms, version);
