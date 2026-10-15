-- Journal records changed: the first stored batch that holds an entry that
-- changes the replay records sets this flag on the update. A later batch that
-- holds a write entry is refused, because a write must come before the record
-- changes.
ALTER TABLE updates ADD COLUMN journal_records_changed INTEGER NOT NULL DEFAULT 0;
