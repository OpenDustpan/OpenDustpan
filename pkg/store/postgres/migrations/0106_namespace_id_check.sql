-- Namespace IDs: refuse an ID without the ns- prefix and one more character,
-- as the SQLite triggers do.
ALTER TABLE namespaces ADD CONSTRAINT namespaces_id_format CHECK (id LIKE 'ns-_%');
