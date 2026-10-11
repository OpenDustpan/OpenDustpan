-- Stack tags: one row for each tag, keyed by the stable stack ID (ADR 19).
-- A rename keeps the ID, so it moves no tag row; a delete removes them with
-- the stack. Tags stay Pulumi concepts in the stack package; they are not the
-- labels of RFC 4.
CREATE TABLE stack_tags (
    stack_id TEXT NOT NULL REFERENCES stacks (id) ON DELETE CASCADE,
    name     TEXT NOT NULL,
    value    TEXT NOT NULL,
    PRIMARY KEY (stack_id, name)
) STRICT;

-- Existing tags live in stacks.tags_json; shred one row per tag.
INSERT INTO stack_tags (stack_id, name, value)
SELECT stacks.id, key, value FROM stacks, json_each(stacks.tags_json);

ALTER TABLE stacks DROP COLUMN tags_json;
