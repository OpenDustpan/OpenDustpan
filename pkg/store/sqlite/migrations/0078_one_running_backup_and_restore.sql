-- One running backup and one running restore for each installation.
--
-- Several server instances can share one database. Each instance kept the
-- rule "one backup at a time" and "one restore at a time" in its own memory,
-- so two instances could start two at once. These partial unique indexes
-- make the database refuse a second running row. The indexed expression is
-- the same value for every running row, so at most one such row exists.
CREATE UNIQUE INDEX backups_one_running ON backups ((state = 'running'))
    WHERE state = 'running';

CREATE UNIQUE INDEX restores_one_running ON restores ((state IN ('checking', 'applying')))
    WHERE state IN ('checking', 'applying');
