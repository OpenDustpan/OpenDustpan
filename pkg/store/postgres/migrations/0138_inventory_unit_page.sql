-- The indexes of the list pages. Each list reads one page after the key of the
-- last row of the page before, so a page reads its own rows and not every row
-- of the namespace.
--
-- The unit list pages in the byte order of the unit ID. The "C" collation is
-- the byte order. The first index gives the units of a namespace in that
-- order. With a label filter, the second index gives the units with the label
-- in that order.
CREATE INDEX inventory_namespace_unit_id ON inventory (namespace, unit_id COLLATE "C");
CREATE INDEX inventory_labels_value ON inventory_labels (namespace, label_key, label_value, unit_id COLLATE "C");

-- The workspace list of the TFE-compatible API pages by name with an offset.
-- This index holds the names of the live workspaces only, so a page skips the
-- rows before its offset in the index and reads no deleted workspace.
CREATE INDEX workspaces_live_name ON workspaces (namespace, name) WHERE deleted_at_ms IS NULL;
