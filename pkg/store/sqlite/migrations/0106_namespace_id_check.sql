-- Namespace IDs: refuse an ID without the ns- prefix and one more character.
-- SQLite cannot add a CHECK constraint to an existing column, so two triggers
-- do it. GLOB is case sensitive; ?* needs at least one character after ns-.
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
