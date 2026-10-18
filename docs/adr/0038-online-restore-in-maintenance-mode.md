# 38. Restore through the server, in maintenance mode

Status: accepted · 2026-10-11 · superseded in part by ADR 151 (expected gaps). The `server:` action names superseded in part by ADR 200. The restore audit entry decided by ADR 202

## Context

ADR 30 makes `opendustpan restore` an offline command. It opens the database
and the blob store directly, needs an empty installation with no server, and is
the only exception to ADR 17 in that ADR. The command exists today
(`cmd/opendustpan`, issue 151).

An offline restore has these problems:

- No authorization, no policy check and no audit. Any person with access to the
  database and the target can replace all state.
- The operator must stop every server instance by hand. A server that still
  runs, or a CLI that still holds a lease, can write into the restored data.
- The checks run in a separate process, at a time that the operator chooses.
  The server cannot refuse a restore that it cannot check.
- A failed restore leaves an empty database. Nothing keeps the data from before
  the restore.

The project owner decided on 2026-10-11 that the server itself does the
restore. It enters maintenance mode, checks that the backup conforms, and
only then applies it. Decisions 3 to 7 below give the mechanism for that
decision.

## Decision

1. **Restore is an API operation.** The installation API (ADR 30 decision 9)
   gets restore routes with the new action `server:restore` on the object
   `@server`. An administrator has the action. The backup comes from the
   configured backup target. The target is never a request field.
2. **The command is a client.** `opendustpan restore` calls the API with
   `--server` and a token, like `opendustpan backup`. No command restores
   offline. This removes the exception to ADR 17 in ADR 30 decision 10. A new
   installation starts on an empty database, and the bootstrap admin token can
   restore into it.
3. **Check before apply.** The server checks the backup before it changes
   any live data:
   - the manifest: format, dialect, schema version, home regions.
   - the key-encryption key (ADR 30 decision 7).
   - every blob that the manifest names: present, with the size and checksum
     from the manifest.
   - the SQL part: size and SHA-256 from the manifest.
   - a load into a staging area (a separate SQLite file, or a separate
     PostgreSQL schema), then checks on the staged data: row counts against the
     manifest, foreign keys, every blob key that the data names exists, and the
     schema version.

   A failed check stops the restore and leaves the live data as it was. A
   check-only request runs the same checks and applies nothing.
4. **Maintenance mode.** The server keeps the installation state in the
   database, so that every instance reads the same value. The states are
   `normal` and `maintenance`. In maintenance mode, the tool APIs (Pulumi
   CLI, TFE-compatible, inventory) answer 503 with `Retry-After` and a message
   that names maintenance. The installation API and the probes stay available.
   The server enters maintenance mode only after the checks pass.
5. **Running work.** The server does not enter maintenance mode while an update
   or a run is active, unless the request sets `force`. With `force`, the
   server cancels the active updates and runs first, and the response names
   them.
6. **Apply.** The server copies the restored blobs into the live blob store.
   Blobs are write-once, so this adds keys and changes none. Then it replaces
   the live SQL data with the staged data in one step (a file rename for
   SQLite, a schema swap in one transaction for PostgreSQL). Live blobs that
   the restored data does not name become orphans for the sweep (issue 203).
7. **Fencing after restore.** The restore increments the failover version of
   every namespace. Leases, update tokens, locks and upload capabilities from
   before the restore stop working, so no client can write data from before
   the restore into the restored data.
8. **A backup before the restore.** Before it applies, the server takes a
   backup of the live data (ADR 30) and names it in the restore result. The
   operator can restore that backup to undo the restore.
9. **Record.** Each restore has a row with its state (`checking`, `applying`,
   `succeeded`, `failed`), the backup ID, the backup taken before it, who asked,
   and the times. It writes a log line (issue 140). The server leaves
   maintenance mode when the restore ends, also after a failure.

This ADR replaces the restore part of ADR 30 decision 10 and step 3 of ADR 30
decision 8 ("Refuse a destination that is not empty"). The other decisions of
ADR 30 stay.

## Consequences

- Restore gets authorization, policy, audit and the e2e rule (ADR 12) like
  every other feature.
- A restore needs free space for the staging area and the backup before it:
  about two times the size of the data during the restore.
- Every instance must check the installation state. A cached value needs a
  short limit, and the apply step must wait until every instance can see
  maintenance mode. The design of that wait is open work.
- CLIs see 503 during a restore. The Pulumi CLI and OpenTofu retry some
  errors, but not all. The e2e suite must show what each supported CLI does.
- The offline restore code in `cmd/opendustpan` goes away. Its checks move
  into the server.
- Disaster recovery on a new machine: start the server on an empty database
  with the same key-encryption key, then restore through the API.
