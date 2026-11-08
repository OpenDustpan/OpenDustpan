\timing on
ALTER TABLE sets_whole ALTER COLUMN vals SET STORAGE EXTERNAL;
SELECT count(*) chunks_unit1, max(length(vals)) max_chunk_bytes FROM sets_chunk WHERE unit_seq=1;
SELECT length(vals) whole_bytes FROM sets_whole WHERE unit_seq=1;
CREATE TEMP TABLE l (k text, lsn pg_lsn);
INSERT INTO l VALUES ('a', pg_current_wal_insert_lsn());
UPDATE sets_whole SET vals = overlay(vals placing '\x0102030405060708'::bytea from 9) WHERE unit_seq=1 AND side=1;
INSERT INTO l VALUES ('b', pg_current_wal_insert_lsn());
UPDATE sets_chunk SET vals = overlay(vals placing '\x0102030405060708'::bytea from 9) WHERE unit_seq=1 AND side=1 AND bucket IN (3, 40, 77);
INSERT INTO l VALUES ('c', pg_current_wal_insert_lsn());
SELECT pg_wal_lsn_diff((SELECT lsn FROM l WHERE k='b'),(SELECT lsn FROM l WHERE k='a')) whole_3value_diff_wal_bytes,
       pg_wal_lsn_diff((SELECT lsn FROM l WHERE k='c'),(SELECT lsn FROM l WHERE k='b')) chunk_3value_diff_wal_bytes;
