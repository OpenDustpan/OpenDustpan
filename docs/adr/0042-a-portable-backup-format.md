# 42. A portable backup format

Status: accepted · 2026-10-11

## Context

ADR 30 captures the SQL part of a backup in the format of its dialect. SQLite
writes a file with `VACUUM INTO`. PostgreSQL writes each table with `COPY`. A
backup restores into the same dialect and the same schema version only. ADR 30
decision 1 and RFC 8 keep "a public format" open. ADR 38 moves restore into
the server, with a staging area and maintenance mode. ADR 39 lets only the
server open the database and the backup target. ADR 37 moves the content of
new Pulumi checkpoints into blobs.

So an operator cannot move an installation from SQLite to PostgreSQL with a
backup. The SQLite part is a whole database file, also when most of its pages
are free.

The project owner asked on 2026-10-11 for a spike of a SQL part that does not
depend on the dialect, and for an ADR if the spike shows no large problem. An
agent session ran the spike on 2026-10-11. The spike opened the databases
directly and checked the results through the domain ports. It did not run the
`storetest` suite.

### The spike

Seed data: 200 stacks with 20 updates each (1 checkpoint and 2 events for each
update), and 200 workspaces with 20 state versions each. The machine had 4
shared cores. Each number comes from one run.

1. **Format.** One file `<table>.jsonl.gz` for each table in `backup.Tables`,
   and a file `manifest.json`. The manifest holds the format (`native` or
   `portable`), the dialect, the schema version, and the row count and the
   column list of each table. The files use gzip at the best level with a fixed
   header time. Two exports of the same data are byte-identical. The largest
   files: `checkpoints` 125,712 bytes (39,329 bytes without the checkpoint
   content), `state_versions` 39,505 bytes, `update_events` 31,019 bytes,
   `updates` 25,990 bytes.
2. **Across dialects.** SQLite to PostgreSQL and back to an export, and
   PostgreSQL to SQLite and back to an export, pass. The row counts and the
   domain rows agree. The SQLite round trip is byte-identical. The export
   changes four things to one form:
   - booleans (`0` and `1` in SQLite, `bool` in PostgreSQL),
   - the checkpoint column `features_json` (SQLite) and `features`
     (PostgreSQL),
   - `backups.ended_at_ms`: `0` in SQLite and `NULL` in PostgreSQL become
     `null`,
   - the white space in `JSONB` values.
3. **Size and time.**

   | Step | Native | Portable |
   |---|---|---|
   | SQLite export | 12,083,200 bytes, 227 ms | 232,149 bytes, 496 ms |
   | SQLite import | 101 ms | 664 ms |
   | PostgreSQL export | 18,430,309 bytes, 380 ms | 232,151 bytes, 525 ms |
   | PostgreSQL import | 452 ms | 3,162 ms |

   The portable part is 52 times (SQLite) and 79 times (PostgreSQL) smaller.
   The export is 1.4 to 2.2 times slower. The import is 6.6 to 7 times slower,
   because it writes one `INSERT` for each row.
4. **Schema versions.** A backup of the same schema version loads. A backup of
   an older version fails with `ErrSchemaMismatch`, unless a flag allows it.
   With the flag, the import loads the columns that the backup holds, and new
   columns keep their defaults. A newer version always fails. A new column
   that needs a computed value loads with a wrong value, and nothing reports
   it.
5. **Incremental.** Without the checkpoint content in SQL (ADR 37), the SQL
   part is 145,766 bytes, 37 percent smaller. The checkpoints file is 69 percent
   smaller. Events and updates are append-only, so an incremental SQL part has
   value. It needs a row-level difference for each table.
6. **Coverage.** The spike added a column `spike_new_col` to `stacks`. The
   export then failed with `table stacks holds column "spike_new_col" that the
   portable format does not cover`, an error that wraps `ErrCoverage`.
   PostgreSQL gives the same result. A covered column that the schema does not
   hold fails too.

The spike also found these risks:

- The import writes all tables in one long transaction (3.2 s on PostgreSQL).
  This does not agree with the staging area of ADR 38.
- The SQLite export reads one table after the other. It does not read from
  one consistent point in time.
- The spike opens the databases directly. ADR 39 lets only the server do that.
- The native PostgreSQL `COPY` list omits four columns: `updates.created_by`
  and `updates.restored_from` (migration 0012),
  `state_versions.restored_from` (0011) and `checkpoints.resource_count`
  (0014). A native backup and restore on PostgreSQL resets them.
- An import of one `INSERT` for each row is 6 to 7 times slower than the
  native import.
- A load of an older schema version trusts the column defaults.

## Decision

This ADR extends ADR 30 and RFC 8. It does not change them.

1. **Two formats.** A backup has a SQL part in one of two formats. The
   `native` format is the format of ADR 30. The `portable` format is the format
   of this ADR. The server writes `portable` by default. The setting
   `--backup-format` (`DUSTPAN_BACKUP_FORMAT`) selects `native`. Restore
   reads both formats.
2. **The portable layout.** The SQL part is one file
   `backups/<id>/sql/<table>.jsonl.gz` for each table in `backup.Tables`. Each
   line is one row: a JSON object with one member for each column. The export
   writes the rows in primary-key order and uses gzip at the best level with a
   fixed header time, so an export of the same data is byte-identical. Blobs
   stay in `blobs/<blob key>`, as ADR 30 decision 5 says.
3. **One form for each value.** The portable format names each column with one
   name for both dialects, and writes each value in one form:
   - a boolean is `true` or `false`,
   - a time is an integer of Unix milliseconds, and a missing time is `null`,
     also where the SQLite schema stores `0`,
   - a byte value is a base64 string,
   - a JSON value is the JSON text. The import compares JSON values as JSON,
     not as text, because PostgreSQL `JSONB` changes the white space.

   Checkpoint content that ADR 37 keeps in blobs is not in the SQL part. Old
   rows that still hold their content in SQL (ADR 37 decision 10) carry it as
   a byte value.
4. **The manifest.** The manifest format number goes to 2. The manifest gets
   the member `sqlFormat` (`native` or `portable`). For `portable`, it holds
   the column list, the row count, the size and the SHA-256 of each table
   file. A manifest with format 1 has no `sqlFormat`, and restore reads it as
   `native`. The dialect in a portable manifest names the source. It does not
   limit the destination.
5. **Coverage.** Each dialect keeps a constant column list for each table: the
   portable name and the dialect column of each column. The export compares
   that list with the live schema (`information_schema` on PostgreSQL,
   `pragma table_info` on SQLite). A column that the list does not cover, or a
   covered column that the schema does not hold, fails the export with
   `ErrCoverage`. A conformance test runs the same comparison in both
   dialects. So a migration that adds a column fails the build until its
   author adds the column to the list.
6. **A consistent read.** The SQLite export reads from the file that
   `VACUUM INTO` writes (ADR 30 decision 3), and then deletes the file. The
   PostgreSQL export reads in the one repeatable-read, read-only transaction of
   ADR 30 decision 4. Both read every table at one point in time.
7. **Only in the server.** The export runs in the server, behind the backup
   routes of ADR 30. The import runs in the server, behind the restore routes
   and maintenance mode of ADR 38 (ADR 39). No command reads or writes a
   portable part.
8. **Import into the staging area.** The import loads the tables in
   foreign-key order into the staging area of ADR 38 (a separate SQLite file or
   a separate PostgreSQL schema). It writes in batches, each in its own
   transaction. On PostgreSQL, it uses `COPY FROM` or multi-row `INSERT`
   statements. The staged data is not live, so a batch that fails only stops
   the restore. The one atomic step stays the swap of ADR 38 decision 6. The
   checks of ADR 38 decision 3 run on the staged data before the swap.
9. **Across dialects.** A portable backup of one dialect restores into the
   other dialect. This changes ADR 30 decision 1 ("the same dialect") for the
   portable format only. A native backup still restores into its own dialect
   only.
10. **Older schema versions.** A portable backup of an older schema version
    restores. The server migrates the staging area to the schema version of the
    backup, loads the rows, and then runs the remaining migrations on the
    staged data. So a migration that computes a new value runs on the restored
    rows, and no column trusts a default. A backup of a newer schema version
    fails with an error that names the version that the operator needs. A
    native backup keeps the rule of ADR 30: the same schema version only.

## Consequences

- An operator can move an installation from SQLite to PostgreSQL, or back,
  with a backup and a restore through the API.
- The SQL part of a backup gets much smaller: in the spike, 52 times (SQLite)
  and 79 times (PostgreSQL). Each backup still copies only the blobs that the
  target does not hold.
- An export is slower than a native export: 1.4 to 2.2 times in the spike. An
  import with one `INSERT` for each row was 6 to 7 times slower. The batches of
  decision 8 must bring it down. The release load run (ADR 40) measures both
  and puts the numbers in the sizing guide.
- Two exports of the same data are byte-identical, so an operator can compare
  two backups by their hashes.
- Each migration that adds, renames or deletes a column must change the column
  list of its dialect. The conformance test enforces this.
- The native PostgreSQL `COPY` list must get the four missing columns. A
  conformance test that compares each copy list with the live schema, as in
  decision 5, keeps it complete. That fix does not wait for this ADR.
- Restore of an older schema version needs the migrations from that version
  on. A migration that drops data stays a one-way step.
- Open work:
  1. The column lists, the coverage check and its conformance test in each
     store package.
  2. The portable export and the manifest format 2 in the `backup` package.
  3. The batched import into the ADR 38 staging area, with the forward
     migration of decision 10.
  4. The setting `--backup-format`.
  5. An e2e step: back up a SQLite installation in the portable format,
     restore it into a PostgreSQL installation, and back. Both CLIs read the
     same state, the Pulumi secret decrypts, and the history is whole.
  6. The backup guide: the two formats, and the move between dialects.
- This ADR defers:
  - an incremental SQL part (a row-level difference for each table),
  - a published specification of the format, so that other tools can read
    it,
  - encryption of the whole backup, backups of one namespace, restore of one
    unit, and restore into another region (RFC 8),
  - an export that reads the tables in parallel.
