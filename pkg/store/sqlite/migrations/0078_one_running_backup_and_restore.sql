-- One running backup and one running restore for each installation.
--
-- Several server instances can share one database. Each instance kept the
-- rule "one backup at a time" and "one restore at a time" in its own memory,
-- so two instances could start two at once. These partial unique indexes
-- make the database refuse a second running row. The indexed expression is
-- the same value for every running row, so at most one such row exists.
--
-- A database can already hold more than one running row: from two starts at
-- once, or from a finish write that failed. The index cannot be built over
-- them, so the store would not open. The migration runs while the server
-- starts, and a start marks every running backup and restore failed anyway.
-- So the migration marks them failed first, with the same error text.
UPDATE backups
SET state = 'failed', error = 'server stopped during backup',
    ended_at_ms = CAST(strftime('%s', 'now') AS INTEGER) * 1000 + CAST(substr(strftime('%f', 'now'), 4, 3) AS INTEGER)
WHERE state = 'running';

UPDATE restores
SET state = 'failed', error = 'server stopped during restore',
    ended_at_ms = CAST(strftime('%s', 'now') AS INTEGER) * 1000 + CAST(substr(strftime('%f', 'now'), 4, 3) AS INTEGER)
WHERE state IN ('checking', 'applying');

CREATE UNIQUE INDEX backups_one_running ON backups ((state = 'running'))
    WHERE state = 'running';

CREATE UNIQUE INDEX restores_one_running ON restores ((state IN ('checking', 'applying')))
    WHERE state IN ('checking', 'applying');
