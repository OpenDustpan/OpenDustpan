-- The stable ID of the unit that an audit entry names (ADR 47), so the
-- history of one unit can be found across a rename or a reuse of its name.
-- An empty value means that the object is not a unit or that the ID is not
-- known, as for every entry written before this migration.
ALTER TABLE audit_entries ADD COLUMN object_id TEXT NOT NULL DEFAULT '';
