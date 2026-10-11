-- Stack IDs: the stable, opaque identifier of a stack (ADR 19). New stacks
-- get stk- IDs from the home region. Existing rows get one here.
-- SQLite cannot change a column to NOT NULL without rebuilding the table,
-- which the foreign keys of updates, checkpoints and update_events forbid
-- while they hold rows. Adding the column NOT NULL with an empty default
-- keeps the rebuild out: the update below fills every row before the unique
-- index lands, and every later write carries an ID.
ALTER TABLE stacks ADD COLUMN id TEXT NOT NULL DEFAULT '';

-- Fill existing rows with stk- IDs. lower(hex(randomblob(10))) is 20 hex
-- characters; 0, 1, 8 and 9 are outside ident's alphabet, so they map to w,
-- x, y and z, which never occur in hex. The mapping is one to one, so
-- distinct inputs stay distinct.
UPDATE stacks SET id = 'stk-' || replace(replace(replace(replace(
    lower(hex(randomblob(10))), '0', 'w'), '1', 'x'), '8', 'y'), '9', 'z')
WHERE id = '';

CREATE UNIQUE INDEX stacks_id ON stacks (id);
