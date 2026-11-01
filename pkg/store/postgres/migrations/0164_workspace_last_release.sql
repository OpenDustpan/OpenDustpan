-- The record of the last released lock of each workspace: the lock ID, the
-- holder, the principal that released the lock, the time of the release and
-- the kind of release. A write of OpenTofu can commit while its answer is
-- lost, and OpenTofu then sends the unlock or the force-unlock again. The
-- record lets the repeat succeed without a change. A workspace without a
-- record keeps empty text, the time 0 and the kind ''. The CHECK refuses a
-- row with another kind.
ALTER TABLE workspaces ADD COLUMN last_release_lock_id text DEFAULT ''::text NOT NULL;
ALTER TABLE workspaces ADD COLUMN last_release_holder text DEFAULT ''::text NOT NULL;
ALTER TABLE workspaces ADD COLUMN last_release_by text DEFAULT ''::text NOT NULL;
ALTER TABLE workspaces ADD COLUMN last_release_kind text DEFAULT ''::text NOT NULL
    CONSTRAINT workspaces_last_release_kind_check
    CHECK (last_release_kind IN ('', 'normal', 'forced'));
ALTER TABLE workspaces ADD COLUMN last_release_at_ms bigint DEFAULT 0 NOT NULL;
