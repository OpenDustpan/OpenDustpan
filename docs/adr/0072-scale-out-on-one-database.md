# 72. Scale out on one database with durable operations

Status: proposed · 2026-10-16 · supersedes ADR 48 decisions 5 to 8 when accepted

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

The project owner answered RFC 36 questions 1 to 7 on 2026-10-16. RFC 36
keeps two questions open: the policy for a missed schedule tick, and the
versions that can run together during a rolling deploy.

## Decision

The project owner decided on 2026-10-16:

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
   The audit sink lease and the audit chain migration claim move to the same
   claim rule.
3. **A schedule is a row.** A periodic job that needs one runner per
   installation is a schedule row with `next_run_at`. One conditional
   `UPDATE` claims a due row and moves `next_run_at` forward. The policy for
   a tick that no instance ran stays open in RFC 36.
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
- ADR 48 decisions 1 to 3 and 9 to 11 define an instance registry, and
  decisions 5 to 8 were its only users. RFC 36 asks the owner whether this
  ADR supersedes them too. Decision 4 of ADR 48 stays until RFC 36 question 9 is
  decided.
