# 3. SQL persistence: hand-written per dialect, sqlc for fixed queries

Status: accepted · 2026-10-08 · two decisions superseded in part by ADR 274

## Context

The database must be swappable (SQLite, PostgreSQL, later MySQL) without an ORM
or a lowest-common-denominator SQL layer.

## Decision

- Each dialect is its own adapter with its own schema, migrations and queries.
  No SQL is shared across dialects.
- Fixed queries are generated with sqlc (development-time only; generated code
  imports only `database/sql` or pgx). Dynamic queries (optional filters,
  sorting) are hand-written in the adapter. sqlc does not translate between
  dialects and is not used for that.
- PostgreSQL uses pgx directly. SQLite uses a pure-Go driver so binaries need
  no cgo.
- Ship SQLite and PostgreSQL first. MySQL waits for a user who needs it.
- Portable schema conventions: `BIGINT`/`TEXT` columns, timestamps as Unix
  milliseconds, JSON as text (or `jsonb` where PostgreSQL benefits), no
  dialect-specific features in the core schema.
- Duplicate inserts use `ON CONFLICT DO NOTHING` and inspect affected rows, so
  adapters never parse driver-specific error codes.

## Consequences

- Every query and migration is written once per dialect, forever. The shared
  `storetest` suite catches behavioural drift between them.
- Separate read and write credentials (replicas) are a later adapter option.
  When added, reads that follow a write in the same flow (for example the CLI
  reading a stack right after saving a checkpoint) must go to the writer, or
  read-your-writes breaks.
