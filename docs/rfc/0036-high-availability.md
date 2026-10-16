---
rfc: 36
title: Scale out to several instances on one database
status: discussing
created: 2026-10-10
---

# RFC 36. Scale out to several instances on one database

Status: discussing. This RFC covers only the scale-out half of high
availability: several server instances on one PostgreSQL database and one
object store, in one region. SQLite stays one instance. Geo replication
between regions stays with ADR 4 and is out of scope.

This RFC proposes an architecture, not a set of patches. It has three parts
that the project owner asked for on 2026-10-16:

1. a definition of the durable-execution capability that the server needs;
2. our own port in `pkg` for that capability, with a conformance suite that
   encodes the known failure modes;
3. a bake-off of two adapters against that suite, our own minimal adapter and
   DBOS Transact, decided on evidence.

The background is in the research note
[durable execution](../research/durable-execution.md), cited below as
"research, part N". An architecture review of 2026-10-16 is a second input.
This RFC cites its findings by their ID (for example "review B1") as claims
until a test confirms them. Code references are at commit 6909035.

## Problem

### The direction

The project owner decided that the server must scale horizontally: several
instances behind a load balancer on one PostgreSQL database and one object
store. A rolling deploy and an autoscale step then start and stop instances
while other instances serve. The owner also set the direction for the
design: instances are stateless, the database is the only place where they
coordinate, background work is durable, and the server keeps no code that
exists only for data from before the first release.

### What works today

These parts already work with several instances:

- Migrations take a transaction-scoped advisory lock
  (`pkg/store/postgres/postgres.go:778-781`).
- Pulumi update leases and OpenTofu workspace locks are rows that the store
  checks in the write transaction.
- Every namespace write carries a `namespace.Fence`. The store reads the
  failover version `FOR SHARE` in the same transaction
  (`inFencedTx`, `pkg/store/postgres/postgres.go:743-760`).
- Maintenance enter is a compare-and-set on the installation row
  (`pkg/maintenance/maintenance.go:195-215`).
- Audit sinks ship under a lease per sink that uses the database clock
  (`pkg/audit/sink_lease.go:16-18`, `queries.sql:1643-1654`).
- Audit checkpoints are create-only blobs.

### What fails

I reproduced two failures with two instances on one PostgreSQL database. Both
tests are on local branches and fail on `main`:

- **A start fails the work of a live instance (#667).** Each start marks every
  running backup and every running restore failed
  (`pkg/dustpan/dustpan.go:1534-1601`). When a restore row is open, it also
  increments the failover version of every namespace. Test: instance A runs a
  backup that waits on its target; instance B starts; A's backup reads failed.
- **Two seals lose OpenTofu state (#668).** The seal at start writes a sealed
  blob before it commits the row, and its sweep deletes every blob of the
  workspace that no row names (`pkg/workspace/seal.go:331-362`). Test:
  instance B seals and stops before its sweep; an older instance writes a
  plain version; instance A writes its sealed blob; B's sweep deletes it; A
  commits a row that names a missing blob.

The code also shows these gaps (read, not yet tested):

- The backup slot, the restore slot and the retention sweep's guard are in
  process memory (`pkg/backup/service.go:132-133`,
  `pkg/backup/restore_service.go:114-115`, `pkg/inventory/sweep.go:114`).
- Only the instance that runs a restore can answer its status while the swap
  hides the row (`restore_service.go:116-123, 247-259`).
- The restore applies right after it enters maintenance mode. Each instance
  caches the installation state for up to 2 s
  (`pkg/maintenance/maintenance.go:42-47`), and only the HTTP layer refuses
  tool calls in maintenance mode (`pkg/dustpan/maintenance.go:47-59`). Store
  writes do not check it.
- Writes at installation scope (tokens, sessions, principals, grants) carry
  no fence, so a restore does not fence them (review B4).
- The PostgreSQL staging schema name lives in one `Store` value
  (`pkg/store/postgres/postgres.go:47-49`). Nothing removes an orphaned
  `restore_stage_*` schema.
- The hourly audit chain check reads the whole log on every instance, and the
  checkpoint mismatch mark is per process (`pkg/audit/checkpoint.go:403-414`).
- The auth failure budget is per process (`internal/authgate/authgate.go`),
  so N instances allow about N times the budget.
- The review reads the code as ending maintenance mode when the restore swap
  commits: the staged data holds the backup's installation row, and the swap
  keeps only the audit chain state (`pkg/store/postgres/restore.go:264-326`).
  A test must confirm this first.

The appendix lists all 31 background jobs, caches and long operations with
their mechanism today and their behavior with two instances.

## The capability we need

### The workloads

The server has a few coarse jobs (research, part 6):

| Work | Today | Duration | Count |
| --- | --- | --- | --- |
| Backup | A row and an in-process slot | Minutes to hours | Few per day |
| Restore | A row, an in-process slot, maintenance mode, a schema swap | Minutes to hours | Rare |
| Audit chain migration | A claim in the chain record | Minutes | Rare |
| Retention sweep, chain check, inactivity disable, sign-in prune | A ticker on every instance | Seconds to minutes | One per period |
| Audit shipping | A lease per sink, a cursor per scope | Continuous | One per sink |

None of them needs many small workflows, durable sleep for days or messages
between workflows. Two need recovery when the instance that runs them stops.
Three need exactly one runner per installation.

### Definition

The server needs these properties. Each one comes from a failure mode in the
research note.

- **Operation.** A unit of durable work with an ID, a kind, an input, a state
  (`pending`, `running`, `succeeded`, `failed`, `canceled`), explicit steps
  with recorded outcomes, an attempt count and the last error. Any instance
  can read its status and list operations.
- **Start key.** Two starts with one key give one operation and the same ID.
  A start can commit in the transaction of a domain write, so a rollback
  leaves no operation.
- **Claim.** One instance at a time runs an operation. The claim has an owner,
  a token that grows on each takeover, and an expiry from the database clock.
  The claim transaction commits at once. No row lock is held while the work
  runs (review A5, research part 5).
- **Fenced step.** A step runs in a transaction that the adapter opens on our
  store handle. The step's domain writes, the namespace fence check, the claim
  token check and the step record commit together, or none of them do. A step
  without a domain write runs at least once, so it must be idempotent
  (research, part 1).
- **Takeover.** When the owner stops, another instance takes the operation
  within one expiry period, with no operator action. A start never takes the
  work of a live owner.
- **Schedule.** A periodic job is a row with `next_run_at`. Claiming a due row
  and moving `next_run_at` forward commit in one statement, so a tick fires
  once across all instances.
- **Retry and poison.** A failed attempt retries with capped exponential
  backoff and jitter. An operation that fails on each attempt stops in a
  terminal `failed` state with its last error.
- **Cancel.** A cancel takes effect at the next step. A canceled operation
  cannot end as succeeded.
- **Restore.** A claim from before a restore cannot write after it.

## Options

### Who decides that an owner is dead

Something must turn silence into "dead" after a timeout. Both shapes below
use the database clock and fence every write with a claim token.

- **L1: an expiry on each claim.** The worker extends the expiry of each
  operation that it runs. One write per running operation per period. No
  registry. The research note recommends this shape, with a fencing epoch
  that grows on each takeover (research, part 6).
- **L2: one lease per instance.** Each instance holds one registration row
  with a lease (ADR 48 decisions 1 and 2). A claim records the owner's
  registration ID and a claim token. Another instance takes over when the
  owner's registration ended plus a clock-skew bound. One write per instance
  per period. The registry also gives status and a peer list. The review
  recommends this shape (review A1, A5, A6).

In both shapes, `SELECT ... FOR UPDATE SKIP LOCKED` appears only in the short
claim transaction. A row lock held for minutes pins a pool connection, holds
back vacuum, fails behind a transaction-mode pooler, survives a dead host for
the TCP keepalive time, and can wait on the restore swap in a cycle that the
deadlock detector cannot see (review A5, research part 5). A singleton
schedule needs no `SKIP LOCKED`: one conditional `UPDATE ... RETURNING` on its
row is the claim.

### Which engine runs operations

1. **Do nothing.** Every instance keeps its tickers and in-process slots.
   #667 and #668 stay data loss.
2. **Our own minimal adapter.** Two or three tables in our migrations
   (operations, steps, schedules), claims in a short transaction, and the
   conformance suite below. The repository already has the same code in three
   places: update leases, the sink lease and the chain migration claim.
3. **DBOS Transact Go** behind our port (MIT, v1.7.0 of 2026-10-15). See the
   bake-off below.
4. **River** (MPL-2.0, v0.49.0 of 2026-10-05). A strong PostgreSQL queue. It
   can insert and complete a job in our own pgx transaction (`InsertTx`,
   `JobCompleteTx`). Its SQLite driver says it is "in early testing", durable
   periodic jobs and workflows are a paid extension, and its migrations would
   have to move into our frozen migrations. Not in the bake-off.
5. **go-workflows** (MIT, v1.4.2 of 2026-06-05). Deterministic replay like
   Temporal, but no start or completion inside our transaction, constructors
   that panic, unqualified table names and one maintainer. Not in the
   bake-off.
6. **A replay engine with its own cluster** (Temporal and similar). Not
   embeddable, and replay engines put the highest cost on workflow code
   (research, part 2). Not in the bake-off.

### How a restore knows that writes stopped

ADR 48 decisions 5 to 8 make the restore ask every live instance over the
network whether it sees maintenance mode. Two alternatives keep the database
as the only coordination point:

1. **A write-path installation check.** Every write transaction reads the
   installation state in the same statement that reads its namespace fence,
   under a shared advisory transaction lock. Maintenance enter changes the row
   under the exclusive form of that lock, so it waits for the writes in
   flight, and every later write sees maintenance and fails. Writes at
   installation scope run the same check.
2. **Confirmation through the registry** (review B3). Each instance writes the
   epoch that its gate enforces into its registration row, after its older
   requests drain. The restore waits until every live registration reports
   the new epoch.

## Proposal

### Principles

1. Instances are stateless and interchangeable. They coordinate only through
   transactional state in our database: no lock service, no leader.
2. Background work and long operations are durable rows. Crash recovery means
   "the claim expired, so another instance can claim it". Start-up steps stop
   being special.
3. The database clock decides every expiry. Every claim carries a token, and
   a write by a stale holder fails in its own transaction, like the namespace
   fence.
4. One small port in `pkg`. Domain packages never import an engine (ADR 35),
   and no claim SQL appears outside the adapters.

Where the code challenges these principles: the inactivity disable and the
sign-in request prune are already safe to run on every instance. The
inactivity disable takes the server audit lock and compares with the database
clock (`pkg/store/postgres/accesstokens.go:281-300`), and the prune is one
`DELETE`. They become schedules only to stop the duplicated work.

### The port

A new tool-neutral package, `pkg/durable` (the name is open), with a sketch
like this:

```go
type Runner interface {
    Start(ctx context.Context, tx Tx, key, kind string, input []byte) (ID, error)
    Get(ctx context.Context, id ID) (Operation, error)
    List(ctx context.Context, f Filter) ([]Operation, error)
    Cancel(ctx context.Context, id ID) error
}

type Step func(ctx context.Context, sc StepContext) error

// StepContext carries the claim token and opens the step transaction on
// our store handle, so the domain write, the fence check and the step
// record commit together.
```

Each tool package registers its kinds and steps through `pkg/dustpan`
wiring. The store adapters implement the port's store side in their own SQL.
A `durabletest` package holds the conformance suite.

### The conformance suite

The suite comes before any adapter. It holds the 18 cases of research,
part 6, and three more:

1. Two instances claim one operation at the same time: exactly one succeeds.
2. A claim on an operation with a live claim fails; a claim after the expiry
   succeeds.
3. An instance whose clock is one hour ahead or behind cannot take or keep a
   claim early or late.
4. An expiry check inside a transaction that started before the expiry sees
   the claim as ended (`now()` against `clock_timestamp()` on PostgreSQL).
5. A holder that lost its claim cannot renew, write a step or complete. Each
   attempt fails in the transaction of the write.
6. A release by a non-holder changes nothing.
7. Recovery leaves the work of a live instance alone, and moves no fence
   (#667).
8. Another instance recovers a dead instance's work within one expiry period,
   with no operator.
9. A newer server version recovers an operation that an older version
   started, or fails it with an error that names the version.
10. Two starts with one key give one operation.
11. N instances fire one tick of a schedule. A tick missed while no instance
    ran follows the stated policy.
12. After a takeover, a step with a stored result does not run again.
13. A start in the transaction of a domain write does not exist after a
    rollback.
14. An operation that fails on each attempt ends `failed` with its last error.
15. Backoff grows, has a cap, and has jitter inside stated bounds.
16. A cancel takes effect at the next step; a canceled operation cannot
    succeed.
17. A holder from before a restore cannot write after the restore.
18. Every claim and operation lists its holder, its expiry and its attempt.
19. A step's domain write, its namespace fence check and its step record
    commit together, and roll back together.
20. Two instances on one PostgreSQL database; one is killed in the middle of
    a step; the other completes the operation.
21. Shutdown leaves no goroutine (goleak).

SQLite runs every case that does not need two instances. A store factory
opens two handles on one database: SQLite opens one file twice, PostgreSQL
one schema twice (review E2). A shared fixture, `internal/servertest`, starts
N real `Run()` servers on one database and can kill and restart one
(review H1).

### The bake-off

Two adapters run against the suite: our own minimal adapter and DBOS. A
throwaway spike on a branch models one long operation (backup) and one
schedule (the retention sweep) through the port on both stores, with two
instances and a kill in the middle of a step. The RFC gets the results before
the owner decides.

What the DBOS v1.7.0 source shows before the spike:

- **Recovery across instances.** Without Conductor, a commercial product, a
  process recovers at start only the workflows of its own executor ID
  (`dbos/dbos.go:903-904`). The default ID is `"local"`. With one ID on every
  instance, instances recover each other's live work, which is #667. With
  unique IDs, the work of a stopped instance stays pending. An adapter would
  need our own liveness signal and the internal recovery call
  (`internals.RecoverPendingWorkflows`). Cases 7 and 8 fail as shipped.
- **Versions.** The default application version is a hash of the binary, and
  recovery takes only workflows of its own version. A rolling deploy strands
  pending work unless we set the version ourselves. Case 9 fails as shipped.
- **Our transaction.** A step commits atomically with its checkpoint only
  when the data source is the same pool as the system database
  (`dbos/datasource.go:390`). The callback receives `dbos.Tx`, not `pgx.Tx` or
  `*sql.Tx`. Our sqlc PostgreSQL code would need a `pgx.Rows` adapter. Our
  sqlc SQLite code needs the concrete `*sql.Rows` and cannot run in it.
- **The pool.** `Shutdown` closes the pool or `*sql.DB` that we pass in
  (`dbos/internal/sysdb/system_database.go:1317`), so DBOS needs a dedicated
  pool, and that loses the atomic mode above. Case 19 fails as shipped.
- **Storage.** On PostgreSQL its tables live in their own schema (`dbos`,
  configurable). On SQLite the schema option is ignored, so its tables land
  next to ours. It runs about 123 migrations of its own at start, with
  `CREATE INDEX CONCURRENTLY`, plpgsql functions and a trigger, and no
  advisory lock.
- **What it does well.** A cron tick fires once because the workflow ID is the
  primary key. Dequeue uses `SKIP LOCKED`. Queue limits are counted in the
  database. It has start keys, cancellation and a patching API. It links
  `google/uuid`, `pgx`, `pgerrcode`, `robfig/cron` and `modernc.org/sqlite`.
  Its logger is an injectable `*slog.Logger`, and its default writes to
  standard output.
- **Determinism.** Recovery runs the workflow function from the start and
  returns stored step results by step ID. Renaming a step function breaks
  recovery.
- **Other backends.** The backend set is closed (PostgreSQL, CockroachDB,
  SQLite). A future database without a DBOS dialect needs our own adapter
  behind the port, so the cost of that adapter is the cost of option 2.

The research note recommends our own adapter in its paradigm (d), a state
machine in domain rows with a fencing epoch (research, part 6). This RFC
keeps the bake-off, because the owner asked for evidence from both adapters.

### Liveness

This RFC proposes L2 for liveness and a claim token for fencing:

- One write per instance per period, not per operation.
- One expiry rule for every claim, the chain migration claim and the sink
  leases.
- The registry also answers "which instances are live" for status, for the
  single-node guard on SQLite and for any later cache peers.

L1 is the fallback if the owner does not want a registry. Both shapes are
suite cases, so the decision changes the adapter, not the port.

### Maintenance and restore

This RFC proposes the write-path installation check (option 1 above):

- It needs no registry, no drain counter and no calls between instances.
- The same statement fences installation-scope writes (review B4).
- Reads during maintenance stay bounded by the 2 s cache, which is safe:
  they change nothing.

I measured its cost on PostgreSQL 16, over loopback, on 4 vCPUs with other
load running, so the numbers are noisy. Each figure is the median of three to
seven runs. The write is a fenced insert: the namespace fence read, one
`INSERT`, and the commit.

| Variant | Serial | 16 parallel |
| --- | --- | --- |
| Fenced write only | 515 µs | 80 µs |
| Check in the fence statement, with a shared advisory lock | 555 µs (+8%) | 87 µs (+8%) |
| Separate `FOR SHARE` read of the installation row | 666 µs (+29%) | 98 µs (+22%) |
| Separate plain read of the installation row | 630 µs (+23%) | 88 µs (+11%) |

The folded check costs no extra round trip, and a shared advisory lock
creates no row-level lock records on the one hot row. The difference is
inside the noise of these runs. A benchmark in the PostgreSQL adapter must
confirm it before the decision.

With this check, the restore apply needs no confirmation from each instance
and no wait for the cache. ADR 48 decisions 5 to 8 become unnecessary, which
needs a superseding ADR.

### Control tables outside the restore swap

Today the installation, backups, restores and sink cursor and lease tables
are part of the data that a restore replaces. The swap keeps only the audit
chain state. This RFC proposes a declared list of bookkeeping tables, each
with "keep" or "clear" across a swap (review B1):

- On PostgreSQL the control tables (operations, steps, schedules,
  registrations, installation, backups, restores) live in a schema that the
  swap never renames.
- On SQLite the swap copies them into the staged file in its fence
  transaction, as it does for the chain state today.
- A coverage test fails for any table that is in neither the backup list nor
  the bookkeeping list.

Then any instance can answer the status of a restore during the swap, and
the in-memory `inflight` map, `recreateAfterSwap`, `PreserveAfterRestore` and
the "superseded" sweep go away. A DBOS schema would sit outside the swap in
the same way.

Small first steps from the review, each with its own test:

- Partial unique indexes allow one running backup and one running restore
  (review B7).
- The staging area becomes a handle on the operation row, staged once
  (review B2).
- `DROP SCHEMA` of the old live data becomes its own step with a
  `lock_timeout` (review B5).
- The maintenance gate fails closed when it cannot read the state
  (review B8).

### Every job in the new model

| Job | Today | New model | Deleted |
| --- | --- | --- | --- |
| Key check at start | Every start | Every start (a per-instance check) | Nothing |
| Backup crash recovery | Every start fails every running row | Takeover of an expired claim | The start-up step, the slot |
| Restore crash recovery and fence | Every start fails every restore and fences | Takeover; the swap fences | The start-up step, the fence at start, the slot, `inflight` |
| Seal of unsealed OpenTofu state | Every start | Deleted (pre-release code) | The step, the sweep, the after-restore seal |
| Audit chain start-up | Every start, can enter maintenance | Every start (a per-instance check); chain record writes become compare-and-set | Nothing |
| Sign-in provider binding | Every start | Every start | Nothing |
| Inactivity disable | Ticker on every instance, hourly | Schedule | The ticker |
| Retention sweep | Ticker on every instance, `TryLock` | Schedule that starts operations | The ticker, `TryLock` |
| Sign-in request prune | Ticker on every instance | Schedule | The ticker |
| Audit chain check and checkpoints | Ticker on every instance, marks in memory | Schedule; result and marks in the database (review C5, C6) | The ticker, the marks in memory |
| Audit chain migration | Claim in the chain record, sleeps | Operation | The hand-made claim, the fixed sleeps |
| Audit sink shipping | Lease per sink | Lease folds into the claim rule; later cursor rows as work items (review C7) | The sink lease table, later |
| Configuration reload | Per instance | Per instance: the file belongs to the instance | Nothing |
| Backup and restore requests | Goroutine and in-process slot | Start an operation; status from the database | The slots, `yieldToEarlierRunner` |
| Token policy cache | 5 s per instance | A join in the credential lookup (review A9) | The cache |
| Maintenance cache | 2 s per instance | Stays for reads; writes check the database | Nothing |
| Auth failure budget | Per instance | See below | Nothing |
| Workspace restore stripe | In-process mutex | Idempotency in the store (review G2) | The striped mutexes |

### Pre-release compatibility code

Nothing has shipped, so this RFC proposes to delete code that exists only for
data from before a change, instead of making it safe for several instances:

- the seal of unsealed OpenTofu state at start, the after-restore seal and the
  unsealed read and copy paths. #668 then goes away with its cause;
- checkpoint content kept in SQL from before checkpoint blobs;
- the stored resource count of older binaries;
- the adoption of checkpoints compressed at an older gzip level;
- empty `CreatedBy` and `TokenID` on updates;
- the fallbacks for missing seed rows (maintenance, audit chain state);
- the refusal message for backup manifest format 1;
- audit hash fields that are conditional on "from before this column".

The owner may also collapse the 52 migrations of each dialect into one
baseline before the first release. That resets the frozen hashes and the
schema list of the binary. After the first release, a rolling deploy runs
two server versions on one schema for minutes, so migrations would follow an
expand-and-contract rule with a test of the previous release against the new
schema (review A12).

### Caching

groupcache-go v3.5.0 (Apache-2.0, last release 2026-03-30) fits
content-addressed checkpoint and state blobs and journal build results. It
does not fit token revocation, policy, sessions or counters, because its
invalidation is best effort. It has no discovery: we push a peer list. Its
peer handler has no authentication, reads a PUT body with no limit, and has a
nil-group panic path. It also brings our first OpenTelemetry dependencies.

This RFC proposes a cache port in `pkg` with an in-process cache of write-once
blob keys first, as a `blob.Store` decorator (review A8). groupcache stays a
later option, only after a load measurement shows a need, and only with peer
authentication and the registry as its peer list.

### Auth failure budget

This RFC proposes to keep the budget per instance for now and to state the
factor in the guide for several instances. A write on every failed attempt
would turn a guessing attack into database load on every instance. A shared
penalty list, written only when a source goes over its budget, can follow.

### Operator rules

A guide for several instances states:

- Every instance uses the same key-encryption key. Do not use
  `--generate-key` on more than one instance.
- Every instance uses the same bootstrap admin token and session secret.
- The backup target is object storage. A `dir:` target and a `file:` audit
  sink are per host.
- Set `--public-url` behind a load balancer.

### Rollout

1. The two-handle store factory and `internal/servertest`, with the #667 and
   #668 reproductions on them.
2. The port and the conformance suite.
3. The bake-off spike and its report in this RFC. The owner decides the
   engine.
4. The control-table schema and the write-path installation check, with the
   superseding ADR for ADR 48.
5. Backup, restore and the chain migration as operations; the start-up
   recovery steps go away.
6. The tickers become schedules.
7. Deletion of the pre-release code, before the first release.

SQLite runs the same port and the same suite with one instance. A guard
refuses a second instance on one SQLite file.

## Open questions

1. Liveness: one lease per instance (L2, a registry) or an expiry on each
   claim (L1)?
2. Maintenance: replace ADR 48 decisions 5 to 8 with the write-path
   installation check, through a superseding ADR?
3. Control tables in their own schema, outside the restore swap and the
   backup scope: does that need a small new ADR?
4. Delete the pre-release compatibility code? Collapse the migrations into
   one baseline per dialect now?
5. The DBOS spike stays on a throwaway branch, and adopting DBOS needs the
   owner's approval of the dependency. Is DBOS still worth the spike, given
   what the source shows?
6. Auth failure budget: per instance and stated in the guide, or a shared
   counter in the database?
7. Caching: an in-process blob cache first, with groupcache only after a load
   measurement?
8. Schedules: skip a tick that no instance ran, or run it once at the next
   start?
9. Rolling deploys: adopt expand-and-contract migrations with a compatibility
   test after the baseline?

## Appendix: background jobs and long operations today

| # | Item | Trigger | State | With two instances |
| --- | --- | --- | --- | --- |
| 1 | Fail running backups | Start | Rows | Fails another instance's live backup |
| 2 | Fail running restores, fence every namespace | Start | Rows | Fails a live restore and moves every fence |
| 3 | Seal unsealed OpenTofu state and sweep | Start | Rows and blobs | Can delete another seal's uncommitted blob |
| 4 | Audit chain start-up | Start | Chain record | A different setting puts the installation in maintenance mode |
| 5 | Sign-in provider binding | Start | Rows | First writer wins |
| 6 | Inactivity disable | Before serve, then hourly | Rows, server audit lock | Serialized; the second run finds nothing |
| 7 | Retention sweep | Hourly | Rows and blobs, in-process `TryLock` | Both instances purge |
| 8 | Sign-in request prune | Every 10 min | Rows | Harmless |
| 9 | Chain check and checkpoint write | Hourly | Blobs; marks in memory | Every instance reads the whole log |
| 10 | Chain migration | After an approval | Claim in the chain record; queue in memory | Only the approving instance runs it |
| 11 | Chain gate cache | Per request, 2 s | Memory | Up to 2 s late |
| 12 | Audit sink shipping | Every 5 s per sink | Cursors and sink leases | One holder per sink |
| 13 | Configuration reload | SIGHUP or a 10 s poll | Memory | One audit entry per instance |
| 14 | File sink reopen | SIGHUP | Local file | Per host |
| 15 | Log sink flush | Ticker | Memory | Per process |
| 16 | Backup | Request, goroutine | Row; slot in memory | Two backups at once |
| 17 | Restore | Request, goroutine | Row; slot and `inflight` in memory | Status only on one instance during the swap |
| 18 | Restore apply | After maintenance enter | — | No wait for other instances |
| 19 | Staging schema name | Restore | Memory | Only the staging instance can apply; orphans stay |
| 20 | Maintenance watcher | Per request, 2 s | Memory over a row | Up to 2 s late; fails open on a read error |
| 21 | Maintenance enter and leave | Request | Row | Enter is compare-and-set; leave is last writer wins |
| 22 | Token policy cache | Per authentication, 5 s | Memory | Up to 5 s late |
| 23 | Workspace striped mutexes | Per request | Memory | The restore stripe guards nothing across instances |
| 24 | Expired-upload sweep in declare | Per declare | Rows and blobs | Best effort |
| 25 | Auth failure budget | Per request | Memory | N times the budget |
| 26 | Body budget | Per request | Memory | Per process |
| 27 | Warning limiter | Per log key | Memory | Per process |
| 28 | Access cache | Per request | Memory | None |
| 29 | Readiness pings, metadata stream | Per request | — | None |
| 30 | Pulumi journal build | In the complete request | Rows and blobs | Update lease in the transaction |
| 31 | Migrations | Store open | Advisory lock | Safe |

## Outcome

None yet.
