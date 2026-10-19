-- The reason that a person gave for an action, for example for a force-unlock.
-- The hash of an entry covers it.
ALTER TABLE audit_entries ADD COLUMN reason text DEFAULT ''::text NOT NULL;
