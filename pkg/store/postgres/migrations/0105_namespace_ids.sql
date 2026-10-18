-- Namespace IDs: the stable, opaque identifier of a namespace. New namespaces
-- get ns- IDs from the home region. Existing rows get one here.
ALTER TABLE namespaces ADD COLUMN id text;

-- Fill existing rows with ns- IDs. The UUID without dashes is 32 hex
-- characters; the first 20 do. 0, 1, 8 and 9 are outside ident's alphabet,
-- so they map to w, x, y and z, which never occur in hex. The mapping is one
-- to one, so distinct inputs stay distinct.
UPDATE namespaces SET id = 'ns-' || translate(
    substr(replace(gen_random_uuid()::text, '-', ''), 1, 20), '0189', 'wxyz')
WHERE id IS NULL;

ALTER TABLE namespaces ALTER COLUMN id SET NOT NULL;

CREATE UNIQUE INDEX namespaces_id ON namespaces USING btree (id);
