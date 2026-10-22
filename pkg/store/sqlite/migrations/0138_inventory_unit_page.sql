-- The unit list reads one page of the units of a namespace in the byte order
-- of the unit ID, after the ID of the last unit of the page before. This index
-- gives the rows of the namespace in that order, so a page reads its own rows
-- and not every row of the namespace.
CREATE INDEX inventory_namespace_unit_id ON inventory (namespace, unit_id);
