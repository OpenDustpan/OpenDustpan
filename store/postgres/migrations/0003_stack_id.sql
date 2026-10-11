-- Stack IDs: the stable, opaque identifier of a stack (ADR 19). New stacks
-- get stk- IDs from the home region. Existing rows get one here.
ALTER TABLE stacks ADD COLUMN id TEXT;

-- Fill existing rows with stk- IDs. The UUID without dashes is 32 hex
-- characters; the first 20 do. 0, 1, 8 and 9 are outside ident's alphabet,
-- so they map to w, x, y and z, which never occur in hex. The mapping is one
-- to one, so distinct inputs stay distinct.
UPDATE stacks SET id = 'stk-' || translate(
    substr(replace(gen_random_uuid()::text, '-', ''), 1, 20), '0189', 'wxyz')
WHERE id IS NULL;

ALTER TABLE stacks ALTER COLUMN id SET NOT NULL;

CREATE UNIQUE INDEX stacks_id ON stacks (id);
