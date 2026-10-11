-- Installation state (ADR 38 decision 4): normal operation or maintenance
-- mode for a restore. One row keyed by id 1, seeded normal: every fresh or
-- migrated database serves tool calls until the server enters maintenance
-- mode. The row carries no fence: it describes the installation, not one
-- namespace. The backup carries the table, so a restore loads the state the
-- backup holds.
CREATE TABLE installation (
    id    INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
    state TEXT    NOT NULL DEFAULT 'normal' CHECK (state IN ('normal', 'maintenance'))
) STRICT;
INSERT INTO installation (id, state) VALUES (1, 'normal');
