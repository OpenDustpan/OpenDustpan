# 24. Where checkpoints live

Status: accepted · 2026-10-10

## Context

ADR 20 decides that the server stores whole checkpoints, that the last write
wins, and that a failed or canceled update leaves the last checkpoint the
engine saved. It leaves the storage and the history of checkpoints to a later
decision ([issue 35](https://github.com/OpenDustpan/OpenDustpan/issues/35)).

Today the table `checkpoints` has one row for each stack. The row holds the
deployment as `BLOB` (SQLite) or `BYTEA` (PostgreSQL). `stack export` returns
that row. `stack export --version N` has nothing to return. The Pulumi CLI sends
it as `GET .../export/{N}`.

The size limit of one checkpoint is 256 MiB (`DefaultMaxCheckpointBytes` in
`stack/updates.go`). The project has no data on real checkpoint sizes.

ADR 19 moves the keys of these tables to the stable stack ID. RFC 18 holds the
open questions about retention, restore and diff. RFC 8 holds backups.

The project owner delegated this decision to an agent session on 2026-10-10.
The owner allowed the agent's reviewed recommendation to become the decision.
The owner can supersede it with a new ADR.

## Decision

1. **One checkpoint for each mutating update.** The key is the stack ID
   (ADR 19) and the update. Each `PATCH` of the running update overwrites the
   row of that update. The latest state of a stack is the checkpoint of its
   highest version. A preview writes no checkpoint (ADR 20).
2. **Export.** `stack export` returns the latest checkpoint. `stack export
   --version N` (`GET .../export/{N}`) returns the checkpoint of update N. It
   answers 404 when version N does not exist or has no checkpoint. It never
   falls back to an older checkpoint.
3. **Storage.** Milestone 1 stores the checkpoint in the SQL row only, in both
   dialects. It uses no blob store and no size threshold. The limit stays at
   256 MiB for each checkpoint. A later migration may add a nullable blob key.
4. **Retention.** The server keeps every checkpoint. The final delete of a
   stack deletes its checkpoints. A retention policy, restore and diff stay in
   RFC 18.
5. **Crash and cancel.** A crash or a cancel leaves the last checkpoint that
   the engine saved for that update (ADR 20, decision 6).
6. **Compression.** The server does not compress in milestone 1. PostgreSQL
   compresses large values (TOAST). SQLite stores them as received.
7. **Backups.** State is in SQL only. So a database backup holds every
   checkpoint (RFC 8).

## Consequences

- Storage grows by one full checkpoint for each mutating update until RFC 18
  adds retention.
- A schema migration replaces the table with one row for each stack. New
  numbered migrations make the change. No applied migration changes. The order
  of these migrations follows the ADR 19 migrations.
- A Pulumi user keeps the command `stack export --version`.
- Open work, not decided here:
  1. A shared blob layout rule for large state (RFC 32 and RFC 1).
  2. E2e steps for `stack export --version` and for export after a cancel, on
     all three supported CLI versions.
  3. Measured checkpoint sizes.
  4. The case of an import that fails before it saves a checkpoint.
