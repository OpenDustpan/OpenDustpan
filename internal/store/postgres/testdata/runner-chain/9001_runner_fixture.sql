-- A fixture migration for the test of the migration runner. The binary does
-- not embed it.
CREATE TABLE runner_fixture (id bigint PRIMARY KEY, note text NOT NULL);
