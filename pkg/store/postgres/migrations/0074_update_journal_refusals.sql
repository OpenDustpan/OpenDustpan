-- Journal refusals: each batch of an update that the server refused or failed
-- to store. A complete with succeeded is refused while the update holds a
-- refusal or a gap in its stored sequence IDs. The build deletes them with
-- the journal rows of the update.
CREATE TABLE update_journal_refusals (
    stack_id     TEXT   NOT NULL,
    update_id    TEXT   NOT NULL,
    arrival      BIGINT NOT NULL,
    sequence_ids TEXT   NOT NULL,
    status       BIGINT NOT NULL,
    reason       TEXT   NOT NULL,
    PRIMARY KEY (stack_id, update_id, arrival),
    FOREIGN KEY (stack_id, update_id) REFERENCES updates (stack_id, id) ON DELETE CASCADE
);
