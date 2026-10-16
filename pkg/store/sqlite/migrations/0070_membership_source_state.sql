-- Source, state and pin of each group membership. source names where the
-- membership comes from: local for a change through the API, scim for a
-- SCIM call, or claim for a group claim at sign-in. source_id names the
-- identity provider or the SCIM token, and is empty for a local membership.
-- state is active or suspended: a suspended membership gives no rights.
-- suspended_at_ms names when the membership was suspended, and is zero while
-- it is active. pinned keeps a restored membership through the next
-- directory sync. Existing rows are local and active.
ALTER TABLE group_members ADD COLUMN source TEXT NOT NULL DEFAULT 'local';
ALTER TABLE group_members ADD COLUMN source_id TEXT NOT NULL DEFAULT '';
ALTER TABLE group_members ADD COLUMN state TEXT NOT NULL DEFAULT 'active';
ALTER TABLE group_members ADD COLUMN suspended_at_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE group_members ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0;
