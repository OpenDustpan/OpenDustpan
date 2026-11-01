# 280. Milestone 7: high availability on one database

Status: accepted · 2026-10-27. Decided by the project owner. Accepts RFC 105 (option 3). Extends ADR 72 (the restore operation and the lease period). Adds milestone 7 to the release gate of ADR 279

Changed by:

- ADR 281 extends decision 5: how the server works with any S3-compatible
  store.
- ADR 286 changes: the words catalog and promotion become self-service and
  failover.
- ADR 298 extends decision 4: what each instance does while the
  PostgreSQL primary fails over.
- ADR 302 extends: milestone 8 follows milestone 7 and uses its model of
  several instances on one database.

## Context

ADR 72 accepted RFC 36: several instances of one installation on one
database, with durable operations, a lease per operation, schedules as rows,
a write-path installation check and rolling deploys. A large part of it is
built. Backups are durable operations that a `worker` process runs, and the
lease is 30 seconds. Some parts are not built yet:

- A restore runs in the process that took the request. It is not a durable
  operation, so the stop of that process stops the restore.
- When the database is not available, the tool-compatible APIs can answer
  500, not an error that tells the CLI to try again later.
- The several-instances guide gives the settings of a layout with more than
  one instance. It does not say which layout the project supports, or what
  recovery figures an operator can expect.

RFC 105 asks how a blob delete and a backup that runs at the same time must
work together. It gives four options, and it proposes option 3.

ADR 248 defines primary and replica installations, request forwarding,
asynchronous replication and manual failover. That model stays in "Later" in
the roadmap.

On 27 October 2026 the project owner answered the questions of an audit of
high availability. Each decision below has the label "(owner)".

## Decision

1. **Scope: one installation, active-active (owner).** Milestone 7 makes one
   installation run active-active: two or more `opendustpan serve` instances
   behind one load balancer, on one PostgreSQL database and one
   S3-compatible object store. The primary and replica installations of
   ADR 248, promotion, forwarding and replication stay in "Later". These are
   out of scope too: active-active across sites, SQLite with more than one
   instance, and the shared cache of ADR 72 decision 9.
2. **Milestone 7 is in the release gate (owner).** Milestone 7 is a part of
   the gate of the first release (ADR 279), with milestone 6. The two
   milestones can run at the same time. The RFC 36 row moves from "Later" to
   milestone 7 in the roadmap.
3. **A restore is a durable operation (owner).** A restore becomes a durable
   operation of ADR 72. A `worker` process on any instance claims it and runs
   it, not the process that took the request. Each step of the restore checks
   the fencing token of the claim and can run again after a takeover. Any
   instance answers the state of a restore from the database. This is the
   restore part of ADR 72, which is not built yet. The several-instances
   guide changes when the code changes.
4. **503 with `Retry-After` when the store is not available (owner).** When the
   database is not available, each tool-compatible API
   answers 503 with a `Retry-After` header, not 500. Each API uses the error
   shape of its tool: `{"code": 503, "message": "..."}` for the Pulumi
   Cloud-compatible API, and a JSON:API error document for the TFE-compatible
   API. The OpenTofu http backend, the inventory API and the installation API
   answer 503 with `Retry-After` too. Before the change merges, an e2e step
   checks that each supported CLI version retries a 503.
5. **The minimum supported topology (owner).**
   - Two or more instances of one installation.
   - PostgreSQL 16 or later.
   - An S3-compatible object store. The topology has no requirement for
     conditional writes.
   - A backup target on an S3-compatible store.
   - The Helm chart refuses the `fs`, `dir:` and `file:` settings when
     `replicas` is more than 1.

   OpenDustpan must work with any S3-compatible store, also with a store
   that does not enforce conditional writes. A later decision defines how
   the server works with such a store. The docs do not say that a named
   third-party store works.
6. **Measured figures, no SLA, no compliance claim (owner).** The docs give
   the recovery figures that the exit test of milestone 7 measures. Each
   figure says that the e2e suite measured it, and on which date. The
   docs never give a figure as a service level agreement. The docs never make
   a compliance claim. Where a text must relate the design to a standard or a
   framework, it says "designed against" that standard.
7. **Blob deletes wait while a backup runs: RFC 105 option 3 (owner).** The
   project accepts the proposal of RFC 105: option 3, in its form with no
   clock, for each delete that the RFC lists as in scope.
   - A deleter deletes a blob that a committed row named only when a read of
     the backups registry, which starts after the commit that deleted or
     replaced that row, finds no running backup.
   - One small port, backed by the backups registry, gives the running
     backup. Each in-scope deleter calls it after its commit and before the
     delete. A failed read means no delete.
   - The backup takes no lock and never waits. The commits of a purge, a
     checkpoint save and a journal build do not change. Only the blob delete
     after the commit can wait.
   - The grace periods stay. A delete needs both its grace and no running
     backup.
   - Each skipped delete is visible to the operator, as the "Visibility"
     section of RFC 105 says.
   - The pull request of a new deleter of a blob that a backup can name adds
     the deleter to the table of RFC 105, and the conformance tests cover it.

   The open questions of RFC 105 are not decided by this ADR. The code
   follows the proposal of the RFC. A new RFC can take up an open question
   again.
8. **The lease stays at 30 seconds (owner).** The lease period of a durable
   operation stays fixed at 30 seconds. No flag and no setting changes it.

## The milestone

`docs/roadmap.md` holds the goal, the exit test and the work items of
milestone 7. The exit test runs three instances of one installation behind
a round-robin TLS proxy with health checks, on PostgreSQL, with the blobs and
the backup target on an S3-compatible server. It has eight steps:
alternation of instances, a kill during work, a kill during a backup, a
restart during a restore, a rolling deploy, schedules, a database outage,
and no worker.

## Consequences

- The release waits for milestone 7 (ADR 279).
- The restore code of today goes away: the start-up step that fails stopped
  restores, the in-process map of running restores and the restore slot.
- The store adapters map the connection errors of their driver to one
  domain error, and each HTTP adapter maps that error to 503. A test in each
  adapter covers it.
- Each in-scope blob deleter reads the backups registry before it deletes. A
  delete can wait for a long backup, so blobs stay in the object store for
  longer.
- The Helm chart gets several replicas, a PodDisruptionBudget and a CI
  profile for them.
- The several-instances guide changes from a set of settings to a supported
  layout, with the minimum topology of decision 5 and the measured figures
  of decision 6.
- RFC 105 gets `status: accepted` and this ADR number.
- ADR 248 does not change. Replication between installations stays a later
  milestone.
