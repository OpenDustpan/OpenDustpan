# 72. Scale out on one database with durable operations

Status: accepted · 2026-10-16 · supersedes ADR 33, ADR 48, ADR 60 and ADR 64 in part (see "Superseded decisions"). Decision 7 superseded in part by ADR 73. Decision 6 superseded in part by ADR 126

## Context

[RFC 36](../rfc/0036-high-availability.md) asks how several server instances
run on one PostgreSQL database and one object store. The project owner
decided that the server must scale horizontally, that instances are
stateless, that the database is the only place where instances coordinate,
and that background work is durable.

Today, a start of one instance fails the backups and restores of the other
instances (#667). Two instances that seal OpenTofu state at start can delete
a sealed blob that a row still names (#668). Writes at installation scope
have no fence during a restore (#684). Three periodic jobs run on every
instance, and the sign-in failure budget is per process.

A spike compared two adapters for durable work: our own adapter and DBOS
Transact Go v1.7.0 (draft #693, not for merge). Our adapter passed every case
on PostgreSQL. The DBOS adapter failed the takeover of the work of a killed
instance, because DBOS recovers the work of another executor only with a
commercial product. RFC 36 gives the results.

[ADR 48](0048-instances-answer-over-the-network.md) decisions 5 to 8 make a
restore ask every live instance over the network whether it sees maintenance
mode. RFC 36 proposes a check in the write path instead.

The project owner answered every question of RFC 36 on 2026-10-16, and
accepted this ADR on the same day.

## Decision

The project owner decided on 2026-10-16. The owner is the decider of each
decision below.

1. **A port for durable operations, with our own adapter.** A tool-neutral
   package in `pkg` defines a port for durable operations: operations with a
   start key, explicit steps, a claim, retry with backoff, cancel and
   schedules. A conformance suite in a `*test` package comes before the
   adapter. The first adapter is our own, in the SQL of each store adapter.
   The project does not adopt DBOS. A DBOS adapter stays possible later
   behind the same port, and it must pass the same suite.
2. **A lease per operation (L1).** Each claimed operation has an expiry on
   the database clock. The worker that runs it extends the expiry. Each claim
   has a fencing token that increases on each takeover. Every step write,
   renewal and completion checks the token in its own transaction. Another
   instance can claim an operation only after its expiry. A start never
   claims an operation that has a live expiry. There is no instance registry.
   This decision supersedes ADR 48 decisions 1 to 3 and 9 to 11. Those
   decisions define a registry with a lease per instance, its renewal and the
   deletion of its rows. The registry gave the list of live instances that
   ADR 48 decisions 5 to 8 call. Decision 4 below replaces those calls, and
   the lease per operation tells whether the owner of an operation is live.
   So no part of the server needs a list of live instances. ADR 48 decision 4
   (the version check at start) and decisions 12 and 13 stay.
   The audit sink lease and the audit chain migration claim move to the same
   claim rule.
3. **A schedule is a row.** A periodic job that needs one runner per
   installation is a schedule row with `next_run_at`. One conditional
   `UPDATE` claims a due row and moves `next_run_at` forward.
   - **A missed tick runs once.** When `next_run_at` is in the past, the
     schedule is due once, whatever the number of ticks that no instance
     ran. The claim sets `next_run_at` to the database clock plus the
     interval, so the missed ticks collapse into one run.
   - **One instance claims it.** The instance that gets the row from the
     conditional `UPDATE` starts the operation, with the schedule and the
     claimed `next_run_at` as its start key. The other instances never run
     the job without a claim.
   - **A random first delay.** At start-up, each instance waits a random
     delay before its first claim of each schedule. The delay is uniform from
     0 to the smaller of 60 s and one tenth of the interval, and each
     schedule draws its own delay. So a start of many instances does not
     claim every schedule at the same moment.
4. **A write-path installation check.** Every write transaction reads the
   installation state in the same statement that reads its namespace fence.
   On PostgreSQL the statement takes a shared advisory transaction lock.
   Maintenance enter changes the installation row under the exclusive form of
   that lock, so it waits for the writes in flight. Every later write sees
   maintenance mode and fails. Writes at installation scope (tokens,
   sessions, principals, grants) run the same check. This decision supersedes
   ADR 48 decisions 5 to 8. A restore applies without a call to each instance
   and without a wait for the cache of each instance.
5. **Control tables stay outside the restore swap.** The installation,
   backups, restores, operations, steps and schedules tables are control
   tables. A restore does not replace them. On PostgreSQL they live in a
   schema that the swap never renames. On SQLite the swap copies them into
   the staged file in its fence transaction. A coverage test fails for a
   table that is in neither the backup list nor the control list.
6. **Pre-release compatibility code goes now.** The server deletes the code
   that exists only for data from older pre-release builds. The list is in
   RFC 36, "Pre-release compatibility code". The first item is the seal of
   unsealed OpenTofu state at start, which deletes the cause of #668.
   - **A fixed audit hash encoding.** No audit entry from before the first
     release has to keep its hash. So the hash encoding of an audit entry
     writes every field in a fixed order, with an empty value when the field
     is not set. No field is optional in the encoding. For `valueCount`, a
     value that is not set and the value 0 stay different. This supersedes
     ADR 60 decision 5 (a new field is optional in the hash encoding) and
     the "when it is above 0" part of ADR 64 decision 10: `fence_version` is
     always in the encoding. The digest of ADR 64 decision 8 then covers the
     fixed field list.
7. **One baseline migration per dialect, now.** The migrations of each
   dialect collapse into one baseline before the first tag. The baseline has
   a new version number, so that the server refuses a database or a portable
   backup from before the baseline, and the error names the cause. This is a
   one-time exception to the rule that an applied migration never changes.
   The frozen hashes of `TestFrozenMigrations` start again from the baseline.
   The owner said: "We can do whatever we want before the first tag."
8. **A shared sign-in failure budget.** The failure budget is a windowed
   counter in the database, shared by all instances. Each failed attempt
   writes once. A successful attempt writes nothing.
9. **An in-process cache first.** A cache port in `pkg` comes first, with an
   in-process cache of write-once blob keys as a `blob.Store` decorator. The
   server adds groupcache only after a load measurement shows a need, and
   only with peer authentication.
10. **Upgrades can skip releases.** The migration runner applies every
    embedded migration that the database does not record, in order. So an
    installation that stopped can start a new release directly, with no
    release in between. A data migration must work from every older schema in
    the chain. Two tests prove the path:
    - A schema test for each released tag applies the migrations of that
      tag, then the new migrations, and compares the schema with a fresh
      install. It runs in `make check`.
    - A full upgrade test from each supported old release straight to the new
      binary writes data with the real Pulumi CLI and OpenTofu, and checks
      the data, the secrets and the held lock. This extends ADR 33 decision
      7, which tests only from the newest tag.
11. **Rolling deploys: expand and contract with K = 2.** Binaries of release
    R-2 to R can run together on one schema.
    - **Expand.** A release adds tables, columns and indexes only in a way
      that older binaries ignore. A release that changes a shape writes both
      the old and the new shape.
    - **Contract after K releases.** A step that deletes or narrows something
      ships 2 releases after the expand that made it unused.
    - **A minimum binary version in the schema.** The schema records the
      oldest binary version that can use it. A contract migration raises it.
      A binary refuses to start only on a schema whose minimum is above its
      own version. The error names both versions. A binary at or above the
      minimum starts on a newer schema. This rule must be in `v0.1.0`,
      because a released binary keeps its rule.
    - **Version rows at start-up.** Each instance writes one row with its
      binary version and start time at start-up, and a clean stop deletes it.
      The row is not a liveness lease: nothing renews it, and nothing reads
      it to decide that an instance is live. The installation API lists the
      rows, and an administrator can delete the row of a crashed instance,
      with its own action. A contract migration that raises the minimum does
      not run while a row below the new minimum exists.
    - **The write path checks the minimum.** The statement of decision 4
      also compares the minimum of the schema with the version of the
      writing binary. A write of a binary below the minimum fails in its own
      transaction. So an old instance that still runs after the minimum goes
      up cannot write.
    - **A test matrix.** CI runs the new schema against each of the last 2
      release binaries, with an old binary and a new binary serving on one
      database.

## Superseded decisions

| ADR | Decision | Superseded by | What changes |
| --- | --- | --- | --- |
| 33 | 1 | Decision 11 | A binary refuses only a schema whose minimum binary version is above its own, not every newer schema. |
| 33 | 5, last sentence | Decision 11 | An older binary at or above the minimum starts on the newer schema. |
| 48 | 1 to 3, 9 to 11 | Decision 2 | No instance registry. A lease per operation tells whether its owner is live. |
| 48 | 5 to 8 | Decision 4 | The write-path check replaces the calls to each instance before a restore applies. |
| 60 | 5, "a new field is optional" | Decision 6 | Every field is in the hash encoding, in a fixed order. |
| 64 | 10, "when it is above 0" | Decision 6 | `fence_version` is always in the hash encoding. |

ADR 48 decisions 4, 12 and 13 stay. For ADR 48 decision 4, a binary supports
a schema when the minimum binary version of the schema is not above its own.

## Consequences

- The start-up steps that fail running backups and restores go away. Another
  instance takes over the work of a stopped instance within one expiry
  period. A start never fails the work of a live instance.
- Backup, restore and the audit chain migration become operations. The
  in-process backup and restore slots, the `inflight` map and
  `yieldToEarlierRunner` go away. Any instance can answer the status of an
  operation from the database.
- The inactivity disable, the retention sweep, the sign-in request prune and
  the audit chain check become schedules. Their tickers on each instance go
  away.
- On SQLite, a stalled write transaction blocks every writer. The port states
  a bound on the step transaction for SQLite, and a step must not hold its
  transaction open while it does slow work outside the database.
- SQLite stays one instance. Without a registry, the SQLite adapter needs
  its own guard against a second process on one file.
- Every write pays for the installation check. A first measurement on
  PostgreSQL 16 gave 8% at the median, inside the noise of the runs. A
  benchmark in the PostgreSQL adapter records the cost when the check lands.
- The sign-in failure budget costs one database write per failed attempt. A
  guessing attack becomes write load on the database. The counter rows need a
  prune.
- `CLAUDE.md` and the store adapter tests state the one-time exception of
  decision 7.
- The server gets no registry table, no `--advertise-address` and no routes
  for calls between instances.
- The version rows of decision 11 are not a registry: nothing renews them,
  and nothing reads them to decide that an instance is live.
- Each shape change takes two releases or more, and the code writes both
  shapes for 2 releases. Each release builds 2 old binaries and runs a mixed
  e2e test per dialect.
- The audit hash of every entry changes once, before the first release.
