# 30. Backups and restore

Status: accepted · 2026-10-11. Superseded in part by ADR 38 (restore) and ADR 39 (`backup check`). Decision 8 superseded in part by ADR 151. Route paths and `server:` action names superseded in part by ADR 210. The backup of one namespace added by ADR 218. The home region of a restore amended by ADR 248 (primary installation). The invariant "keys are never reused" of decision 2 made true by construction by ADR 281

## Context

[RFC 8](../rfc/0008-backups-and-restore.md) proposes backups as a feature of
the product. A brief of 2026-10-11 checked the RFC against the code. It
informed this ADR.

The state of a team lives in three places. SQL holds namespaces, Pulumi
stacks, updates, checkpoints (ADR 24), wrapped data keys (ADR 23), the
inventory and the OpenTofu metadata. The blob port holds OpenTofu state
content (ADR 26). The key-encryption key lives outside both (ADR 23). The blob
port has `Put`, `Get` and `Delete`. A caller writes each key once. A committed
state version never names a missing blob, because the server writes the blob
first (ADR 26). No `backup` command, no `restore` command and no
installation-level action exist. ADR 17 asks for a REST API for each feature
and says that the shape of the OpenDustpan API is a separate RFC. ADR 3 keeps
SQL separate for each dialect. ADR 31 adds an S3-compatible blob adapter.

The project owner delegated this decision to an agent session on 2026-10-10.
The owner allowed the reviewed recommendation of the agent to become the
decision. The owner can supersede it with a new ADR.

## Decision

This ADR accepts RFC 8 with the scope below.

1. **Scope of milestone 3.** The server makes and restores a backup of the
   whole installation. A backup restores into the same dialect and the same
   schema version only. This ADR defers these items: schedules and retention
   (RFC 7), backups of one namespace, GCS and Azure targets, a public format,
   encryption of the whole backup, restore of one unit (RFC 18) and restore
   into another region.
2. **Consistency.** A backup reads the SQL data in one read transaction. It
   then copies each blob that the SQL data names. Blobs are immutable and keys
   are never reused, so the copy is consistent while the server writes. The
   backup skips blobs that the SQL view does not name. Retention (RFC 18) must
   not delete a blob that a running backup can still copy.
3. **SQLite capture.** The backup runs `VACUUM INTO` on a new file. It adds no
   dependency.
4. **PostgreSQL capture.** The backup reads each table with `COPY` through
   `pgx`, in one repeatable-read, read-only transaction. It does not run
   `pg_dump`. The table list is a constant of the store package. A conformance
   test fails when a table is missing from the list.
5. **Target.** A backup target is a `blob.Store`. The built-in targets are a
   local directory and, with ADR 31, S3-compatible storage. The target holds
   `blobs/<blob key>`, `backups/<id>/db` and `backups/<id>/manifest.json`. The
   backup does not copy again a blob that the target already holds
   (`ErrExists`). The server writes the manifest last. Restore refuses a backup
   that has no manifest. The blob port gets no `List` now. The operator passes
   the backup ID.
6. **Manifest.** The manifest is JSON. It holds a format number, the backup ID,
   the creation time, the server version, the dialect, the schema version, the
   home regions, the wrapper IDs, the size and SHA-256 of the SQL part, and the
   key, size and MD5 of each blob. The backup compares each blob with the MD5
   and size in the SQL data while it copies. The format is internal in this
   milestone. `opendustpan backup check` checks that the target holds every
   object that the manifest names, with the recorded size.
7. **Key-encryption key.** A backup never holds the key-encryption key. Restore
   reads the key as the server does (ADR 23). If the manifest lists wrapper
   IDs, restore stops before it writes anything, unless the key gives one of
   them and opens one data key. The error names `DUSTPAN_KEY_ENCRYPTION_KEY`.
   The backup command prints that a backup cannot decrypt secrets without the
   key. A backup holds OpenTofu state in plain text, because the server does
   not encrypt it. The documents tell the operator to protect the target.
8. **Restore.** Restore does these steps in this order:
   1. Read the manifest. Refuse an unknown format, another dialect or another
      schema version. The error names the version that the operator needs.
   2. Check the key (decision 7).
   3. Refuse a destination that is not empty.
   4. Copy the blobs. `ErrExists` is an error.
   5. Load the SQL data. For SQLite, restore checks a temporary file and
      renames it. For PostgreSQL, restore migrates the empty schema and loads
      all tables in one transaction, in foreign-key order.
   6. Compare the row counts with the manifest.

   A restore that stops early leaves an empty database. It never leaves a
   database that names missing blobs. Restore keeps the home region and the
   failover version of each namespace. It warns when the `--region` flag is not
   among the regions of the manifest.
9. **Installation API.** Backups and metrics (ADR 32) share one tool-neutral
   HTTP adapter: the package `api/server`. `dustpan.go` mounts it at
   `/api/server/`, a pattern more specific than the Pulumi `/api/` pattern, as
   it does for `api/inventory`. The adapter has its own route table and its own
   `NewHandler`. The route test of ADR 17 covers every route. The object of
   each action is `@server`, the installation. All routes need a token.
   - `POST /api/server/backups` starts a backup and answers 202 with the ID.
     Only one backup runs at a time. A second request answers 409.
   - `GET /api/server/backups` lists the backups that this installation
     made, newest first. A new table `backups` in each dialect keeps one row
     for each backup: the ID, the state, the start and end times, the target
     and the error text for an operator. The row survives a restart. A server
     that stops during a backup marks the row `failed` at the next start.
   - `GET /api/server/backups/{id}` answers the state (`running`, `succeeded`
     or `failed`) and, when done, the manifest.
   - The three routes need the new action `server:backup`. An administrator has
     it. Other roles can get it from RFC 19.
   - The target is a server setting (`--backup-target`, with credentials in the
     environment), never a request field. So a token cannot send data to
     another place.
10. **Commands.** `opendustpan backup` is a client of the API. It needs
    `--server` and a token from `DUSTPAN_ADMIN_TOKEN`. `opendustpan backup
    check --backup <id>` reads the target and needs no server.
    `opendustpan restore --from <target> --backup <id>` is an offline command,
    because it needs an empty installation with no server. It is the only
    exception to ADR 17 in this ADR. The target syntax is `dir:/path` or
    `s3://<bucket>[/<prefix>]` (ADR 31), for `--backup-target` and `--from`.
    `DUSTPAN_BACKUP_TARGET` sets the target.
11. **Order of work.** The conformance suite `RunBackup` lands before the store
    code. Each command and each route lands with its e2e step first (ADR 12).

## Consequences

- Open work:
  1. `storetest.RunBackup` (a round trip over every table, the missing-table
     test, a test that an export does not tear, the `backups` table rows),
     then the SQLite and PostgreSQL code and the migration for `backups`.
  2. A `backup` package for the manifest, the blob copy and the restore order,
     with unit tests for the key check, the empty-destination check, a wrong
     MD5, a missing manifest and a newer schema version.
  3. The package `api/server` with the three backup routes, the action
     `server:backup`, the object `@server`, and the route test entries
     (ADR 17). ADR 32 adds the metrics route to the same package. The first of
     the two changes creates the package.
  4. The commands `backup`, `backup check` and `restore`, and the flag
     `--backup-target`.
  5. An e2e step that runs the real binary: backup, stop, restore with the same
     key into an empty directory, then read the Pulumi secret, the OpenTofu
     state and the history. A step with a wrong key. The step runs on SQLite,
     on PostgreSQL and on an S3-compatible server.
  6. Backup documents: the key, the plain-text state, and the restore order.
- A lost key-encryption key still means lost secrets, and a backup does not
  change that.
- A new migration needs an entry in the table list of its dialect. The
  conformance test enforces this.
- A restore to another schema version needs the matching release first. ADR 33
  covers upgrade checks, and the way back from an upgrade is a restore.
- With an S3 target, one object has a limit of 5 GiB (ADR 31). A SQLite file or
  a PostgreSQL export above this limit fails with a clear error.
- The `backups` table lists the backups that this installation made. It does
  not list backups that another installation wrote to the same target. That
  needs `List` on the blob port. Retention and the scheduled restore test of
  RFC 8 wait for `List` and for RFC 7.
- RFC 8 stays open for: a public format, encryption of the whole backup,
  restore of one unit, and restore into another region.
- The exit test of this part of milestone 3 is this. On Linux, on SQLite and on
  PostgreSQL, the e2e suite takes a backup while the Pulumi and OpenTofu
  scenarios write. It restores the backup into an empty installation with the
  same key. Both CLIs then read the same state, the Pulumi secret decrypts, and
  the history is whole. A restore with a wrong key stops before it writes and
  names `DUSTPAN_KEY_ENCRYPTION_KEY`. A restore with an S3-compatible target
  passes once. The route test finds the action `server:backup` on each backup
  route.
