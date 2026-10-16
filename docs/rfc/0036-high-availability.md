---
rfc: 36
title: Scale out to several instances on one database
status: discussing
created: 2026-10-10
---

# RFC 36. Scale out to several instances on one database

Status: discussing. The project owner decided questions 1 to 7 on
2026-10-16 (see "Decisions"). Questions 8 and 9 stay open, and each has a
proposal below. The decided parts go to an ADR. This RFC stays open until
the owner answers questions 8 and 9.

This RFC covers only the scale-out half of high availability: several
server instances on one PostgreSQL database and one object store, in one
region. SQLite stays one instance. Geo replication between regions stays
with ADR 4 and is out of scope.

The background is in the research note
[durable execution](../research/durable-execution.md), cited below as
"research, part N". The
[architecture review of 2026-10-16](../architecture-reviews/2026-10-16.md)
and its [verdicts](../architecture-reviews/2026-10-16-verdicts.md) are a
second input. This RFC cites their findings by ID (for example "review B1").
Findings that a test or a second reading verified have their own issue, and
this RFC cites that issue as a fact. Code references are at commit 6909035.

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

Two failures are reproduced with two instances on one PostgreSQL database.
Both tests are on local branches and fail on `main`:

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
  commits a row that names a missing blob. A force-unlock fence blob, which
  records nothing before its `Put`, has the same exposure.

Other verified failures, with their issue where one exists:

- A restore ends maintenance mode when its swap commits: the staged data
  holds the backup's installation row, and the swap keeps only the audit chain
  state (`pkg/store/postgres/restore.go:264-326`). Reproduced on both
  dialects (#678). Fixed on `main` by #695: the swap now keeps the
  installation state of the target.
- A running backup races the retention sweep: a purge that commits after the
  backup's snapshot deletes blobs that the backup still copies (#683).
- Writes at installation scope (tokens, sessions, principals, grants) carry
  no fence, so they can commit during a restore (#684).
- Any instance can renew the audit chain migration claim
  (`queries.sql:1492-1507`, #685).
- Nothing stops two running backups or two running restores across
  instances; partial unique indexes would (#690).
- The maintenance watcher fails open: before its first successful read it
  answers `normal`, and after a read error it keeps the last state
  (`pkg/maintenance/maintenance.go:634-654`, #685).

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
- The PostgreSQL staging schema name lives in one `Store` value
  (`pkg/store/postgres/postgres.go:47-49`). Nothing removes an orphaned
  `restore_stage_*` schema.
- The hourly audit chain check reads the whole log on every instance, and the
  checkpoint mismatch mark is per process (`pkg/audit/checkpoint.go:403-414`).
- The auth failure budget is per process (`internal/authgate/authgate.go`),
  so N instances allow about N times the budget.

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
  store handle. The step's domain writes, the namespace fence check, the
  installation check, the claim token check and the step record commit
  together, or none of them do. A step without a domain write runs at least
  once, so it must be idempotent (research, part 1).
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

## Decisions

The project owner decided these questions on 2026-10-16. Each one goes into
the ADR for this RFC.

1. **Adapter: our own minimal adapter.** The durable-operations port in
   `pkg` gets one adapter that we write, with the conformance suite below.
   DBOS is not adopted. A DBOS adapter stays possible later behind the same
   port. Decider: the project owner, 2026-10-16.
2. **Liveness: a lease per job (L1).** Each claimed operation has an expiry
   on the database clock. Its worker extends the expiry. Each claim has a
   fencing token that grows on each takeover, and every step write checks
   it. There is no instance registry. Decider: the project owner,
   2026-10-16. (Question 1.)
3. **Maintenance: the write-path check.** Every write transaction checks the
   installation state in the same statement as its namespace fence. Writes at
   installation scope run the same check. This replaces ADR 48 decisions 5
   to 8, through a superseding ADR. Decider: the project owner, 2026-10-16.
   (Question 2.)
4. **Control tables move out of the restore swap.** On PostgreSQL they live
   in a schema that the swap never renames. On SQLite the swap copies them
   into the staged file. A coverage test fails for a table that is in
   neither the backup list nor the control list. Decider: the project owner,
   2026-10-16. (Question 3.)
5. **Pre-release code goes now.** The compatibility code for data from older
   pre-release builds is deleted now. The migrations of each dialect collapse
   into one baseline now, not at the first tag. The owner said: "We can do
   whatever we want before the first tag." Decider: the project owner,
   2026-10-16. (Question 4.)
6. **The DBOS spike is closed.** Decision 1 answers question 5. Decider: the
   project owner, 2026-10-16.
7. **Sign-in failure budget: a shared counter.** The budget is a windowed
   counter in the database, shared by all instances, with one write per
   failed attempt. Decider: the project owner, 2026-10-16. (Question 6.)
8. **Cache: an in-process cache port first.** A cache port in `pkg` with an
   in-process cache of write-once blob keys comes first. groupcache comes
   only after a load measurement shows a need. This was the proposal of this
   RFC, and the owner raised no objection on 2026-10-16. (Question 7.)

## Design for the decided parts

### Principles

1. Instances are stateless and interchangeable. They coordinate only through
   transactional state in our database: no lock service, no leader, no
   instance registry.
2. Background work and long operations are durable rows. Crash recovery means
   "the claim expired, so another instance can claim it". Start-up steps stop
   being special.
3. The database clock decides every expiry. Every claim carries a token, and
   a write by a stale holder fails in its own transaction, like the namespace
   fence.
4. One small port in `pkg`. Domain packages never import an engine (ADR 35),
   and no claim SQL appears outside the store adapters.

The inactivity disable and the sign-in request prune are already safe to run
on every instance. The inactivity disable takes the server audit lock and
compares with the database clock
(`pkg/store/postgres/accesstokens.go:281-300`), and the prune is one
`DELETE`. They become schedules only to stop the duplicated work.

### Liveness with a lease per job

- A claim sets the owner, increments the claim token and sets the expiry to
  the database clock plus the lease period, in one short transaction that
  commits at once.
- The worker extends the expiry of each operation that it runs. An extension
  succeeds only when the claim token is still its own. One write per running
  operation per period. The number of running operations is small (see "The
  workloads").
- Another instance can claim an operation only after its expiry on the
  database clock. A start never claims an operation with a live expiry, so
  #667 goes away.
- Every step write checks the claim token in its own transaction. A holder
  that lost its claim cannot write, renew or complete.
- The audit sink lease and the audit chain migration claim fold into the same
  claim rule. The hand-made claims go away.

Without a registry, the server has no list of live instances. Three users of
that list need another source:

- Status: the operations list shows the owner and the expiry of each claim.
- The single-instance guard on SQLite: the SQLite adapter must refuse a
  second process on one file without a registry. The ADR states the rule. The
  mechanism is an issue of its own.
- groupcache peers: groupcache is deferred (decision 8). If it comes later,
  its peer list is a new question.

### Where the SQL lives

The proposal adds tables in both dialects: operations, steps and schedules.
Today each dialect has its own SQL and its own adapter code (ADR 3). The
owner has not yet decided on a shared store core with a policy per dialect
(review E1). That decision changes how much code these tables cost: with a
shared core, the claim and fence logic is written once.

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

The suite comes before the adapter. It holds the 18 cases of research,
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
    ran follows the policy of question 8.
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

The bake-off found that the "stalled holder" case differs per dialect. On
SQLite, one stalled write transaction blocks every writer, so no other worker
can take over or even claim. On PostgreSQL, another instance takes over and
the late write is fenced. On SQLite, the case needs a bound on the step
transaction (a statement or transaction timeout) so that a stall ends. The
port states this. A step that does slow work outside the database must not
hold its transaction open while it waits.

### Maintenance and restore

Every write transaction reads the installation state in the same statement
that reads its namespace fence, under a shared advisory transaction lock.
Maintenance enter changes the row under the exclusive form of that lock, so
it waits for the writes in flight, and every later write sees maintenance
and fails. Writes at installation scope run the same check (#684).

- It needs no registry, no drain counter and no calls between instances.
- Reads during maintenance stay bounded by the 2 s cache, which is safe:
  they change nothing.
- The restore apply needs no confirmation from each instance and no wait for
  the cache.

A first measurement on PostgreSQL 16, over loopback, on 4 vCPUs with other
load running, gave these numbers. They are noisy. Each figure is the median
of three to seven runs. The write is a fenced insert: the namespace fence
read, one `INSERT`, and the commit.

| Variant | Serial | 16 parallel |
| --- | --- | --- |
| Fenced write only | 515 µs | 80 µs |
| Check in the fence statement, with a shared advisory lock | 555 µs (+8%) | 87 µs (+8%) |
| Separate `FOR SHARE` read of the installation row | 666 µs (+29%) | 98 µs (+22%) |
| Separate plain read of the installation row | 630 µs (+23%) | 88 µs (+11%) |

The folded check costs no extra round trip, and a shared advisory lock
creates no row-level lock records on the one hot row. The difference is
inside the noise of these runs. A benchmark in the PostgreSQL adapter
records the cost when the check lands.

### Control tables outside the restore swap

Today the installation, backups, restores and sink cursor and lease tables
are part of the data that a restore replaces. The swap keeps only the audit
chain state, so a restore ends maintenance mode when its swap commits
(#678). A declared list of control tables, each with "keep" or "clear"
across a swap, fixes this (review B1):

- On PostgreSQL the control tables (operations, steps, schedules,
  installation, backups, restores) live in a schema that the swap never
  renames.
- On SQLite the swap copies them into the staged file in its fence
  transaction, as it does for the chain state today.
- A coverage test fails for any table that is in neither the backup list nor
  the control list.

Then any instance can answer the status of a restore during the swap, and
the in-memory `inflight` map, `recreateAfterSwap`, `PreserveAfterRestore` and
the "superseded" sweep go away.

Small first steps from the review, each with its own test:

- Partial unique indexes allow one running backup and one running restore
  (#690).
- The staging area becomes a handle on the operation row, staged once
  (review B2).
- `DROP SCHEMA` of the old live data becomes its own step with a
  `lock_timeout` (review B5).
- The maintenance gate fails closed when it cannot read the state.
- A purge does not delete blobs that a running backup still needs, and
  background work pauses in maintenance mode (#683).

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
| Auth failure budget | Per instance | A windowed counter in the database | The per-process counter |
| Workspace restore stripe | In-process mutex | Idempotency in the store (review G2) | The striped mutexes |

### Pre-release compatibility code

Nothing has shipped, so the code that exists only for data from before a
change goes, instead of being made safe for several instances:

- the seal of unsealed OpenTofu state at start, the after-restore seal and the
  unsealed read and copy paths. #668 then goes away with its cause;
- checkpoint content kept in SQL from before checkpoint blobs;
- the stored resource count of older binaries;
- the adoption of checkpoints compressed at an older gzip level;
- empty `CreatedBy` and `TokenID` on updates;
- the fallbacks for missing seed rows (maintenance, audit chain state);
- the refusal message for backup manifest format 1;
- audit hash fields that are conditional on "from before this column".

The migrations collapse into one baseline per dialect instead of 53 each
(review A12). The baseline gets a new version number, so that a database or
a portable backup from before the baseline is refused by name. The collapse
resets the frozen hashes of `TestFrozenMigrations` and the schema list of the
binary. It is a one-time exception to the rule that an applied migration
never changes, and the ADR records it.

### Sign-in failure budget

The budget is a windowed counter row per source in the database. A failed
attempt increments the counter of its window in one statement. An attempt
reads the counter before it checks the credential. A successful attempt
writes nothing. The cost is one write per failed attempt, so a guessing
attack becomes database writes on every instance. The counter rows need a
prune, which can be a schedule.

### Caching

groupcache-go v3.5.0 (Apache-2.0, last release 2026-03-30) fits
content-addressed checkpoint and state blobs and journal build results. It
does not fit token revocation, policy, sessions or counters, because its
invalidation is best effort. It has no discovery: we push a peer list. Its
peer handler has no authentication, reads a PUT body with no limit, and has a
nil-group panic path. It also brings our first OpenTelemetry dependencies.

The cache port in `pkg` comes first, with an in-process cache of write-once
blob keys as a `blob.Store` decorator (review A8). groupcache stays a later
option, only after a load measurement shows a need, and only with peer
authentication and a peer list.

### Operator rules

A guide for several instances states:

- Every instance uses the same key-encryption key. Do not use
  `--generate-key` on more than one instance.
- Every instance uses the same bootstrap admin token and session secret.
- The backup target is object storage. A `dir:` target and a `file:` audit
  sink are per host.
- Set `--public-url` behind a load balancer.

## Proposal for question 8: a missed schedule tick

### The problem

A schedule row has `next_run_at`. When no instance runs for longer than one
interval, for example during an outage or a full stop for an upgrade, one or
more ticks pass with no run. At the next start, every instance starts its
scheduler at about the same time. The owner does not want a thundering herd
at start-up.

The jobs that become schedules are the retention sweep, the audit chain
check, the inactivity disable, the sign-in request prune and the counter
prune. Each one is a sweep: one run does all the work that the missed runs
would do. None of them needs to know how many ticks it missed.

### The proposal

1. **A catch-up runs at most once.** When `next_run_at` is in the past, the
   schedule is due once, whatever the number of missed ticks. The claim sets
   `next_run_at` to the database clock plus the interval, not to the old
   `next_run_at` plus the interval. So the missed ticks collapse into one run,
   and the schedule then continues at its normal interval.
2. **One instance claims it.** The claim is one conditional statement on the
   schedule row:

   ```sql
   UPDATE schedules
      SET next_run_at = now() + interval, last_claimed_at = now()
    WHERE kind = $1 AND next_run_at <= now()
   RETURNING next_run_at;
   ```

   The instance that gets a row starts the operation, with the schedule kind
   and the claimed `next_run_at` as its start key. A second start with that
   key gives the same operation. The operation has a lease like every other
   operation, so if the instance stops, another instance takes it over.
3. **The first claim waits a random delay.** At start-up, each instance waits
   a random delay before its first claim of each schedule. The delay is
   uniform from 0 to a bound. The bound is the smaller of 60 s and one tenth
   of the interval. Each schedule draws its own delay, so the catch-ups of
   different schedules do not start at the same moment on one instance.
4. **The other instances never run it.** An instance that gets no row from
   the claim does nothing for that tick. It never runs the job without a
   claim, and it never retries the claim before the next `next_run_at`.

The cost: one column (`last_claimed_at`) for status, the random delay in the
scheduler loop, and two cases in the suite:

- Three instances start together after a stop of three intervals. The job
  runs once. `next_run_at` is one interval after the claim.
- The first claim attempts of the instances spread over the delay bound. A
  test with a fixed random source checks the bound.

### Alternatives

- **Skip every missed tick.** The schedule moves `next_run_at` to the next
  boundary after now and runs then. No run at start-up at all. The cost: an
  inactive token can stay enabled for up to one more interval after a long
  stop, and a retention sweep can wait one more interval.
- **Run each missed tick.** A backlog of runs at start-up. None of the jobs
  needs it, and it is the herd that the owner does not want.

The proposal is the first option above (catch-up once). It gives the work of
the missed ticks in one run, and the delay spreads the claims. A schedule
that must count missed ticks does not exist today. If one comes, it needs
its own policy.

## Proposal for question 9: upgrades and rolling deploys

The owner asked: "Can we do better than N-1? Can we make it possible for
upgrades to skip versions?" These are two different questions:

1. **Upgrade path.** Can an installation that stopped on an old release start
   the new release directly, with no release in between?
2. **Coexistence.** Which binary versions can run together on one schema
   while a rolling deploy replaces them?

### Part 1. The upgrade path: skipping versions

The migration runner already applies every embedded migration that the
database does not record, in order
(`pkg/store/postgres/postgres.go:798-815`,
`pkg/store/sqlite/sqlite.go:1072-1089`). So a stopped upgrade can skip any
number of releases today. What is missing is the proof.

Proposal:

- **An upgrade test from each supported old release straight to the new
  one.** ADR 33 decision 7 tests only from the newest tag. The test grows to
  a list of old tags. For each tag it builds that release, writes data with
  the real Pulumi CLI and OpenTofu, stops it, starts the new binary on the
  same database, and checks the data, the secrets and the held lock.
- **A cheaper schema test for every tag.** For each old tag, the test applies
  that tag's migrations, then the new migrations, and compares the schema
  with a fresh install of the new binary. It needs no CLI and runs in
  seconds per tag, so it can run in `make check` for all tags.
- **Data migrations stay idempotent and ordered.** A migration that rewrites
  data must work from any older schema in the chain, not only from the
  release just before it. A slow data migration can run as a durable
  operation after start, so that a large skip does not hold the start.
- **A later baseline sets a floor.** If the migrations collapse again after
  the first tag, the new baseline refuses an older database by name and names
  the release to upgrade through. The proposal is to collapse no migration
  after the first tag unless the owner decides it.

Cost: the schema test is small and runs on every change. The full upgrade
test takes one build and one e2e run per old tag and per dialect. Today one
run has a limit of 20 minutes. To keep the cost bounded, the full test runs
from the first tag and from the last few tags, and the schema test covers
every tag.

### Part 2. Coexistence: running several versions together

A rolling deploy starts new instances while old instances still serve.
Today ADR 33 decision 1 makes an older binary refuse any schema that it does
not know. So the first new instance migrates, and every old instance fails
at its next start (ADR 33 decision 5). An old instance that does not
restart keeps serving on a schema that it does not know, with no check.

Proposal: expand and contract, with each contract delayed by a support
window of K releases.

1. **Expand.** A release adds tables, columns and indexes only in a way that
   older binaries ignore. New columns are nullable or have a default. A
   release that changes a shape writes both the old and the new shape.
2. **Contract later.** A step that removes or narrows something (drop a
   column or a table, add `NOT NULL`, change a type) ships K releases after
   the expand that made it unused, not in the same release.
3. **The schema records a minimum binary version.** The schema stores the
   oldest binary version that can use it. A contract migration raises it. A
   binary below the minimum refuses to start, and the error names both
   versions. A binary at or above the minimum starts on a newer schema. This
   changes ADR 33 decision 1 from "refuse any newer schema" to "refuse a
   schema whose minimum is above this binary", which needs a superseding
   ADR.
4. **Each instance writes its version at start-up.** At start-up, each
   instance writes one row with its binary version and its start time. The
   row is not a liveness lease: nothing renews it. A clean stop deletes it.
   The installation API lists the rows, and an administrator can delete a row
   of a crashed instance, with its own action (ADR 17).
5. **A contract runs only when no older instance is recorded.** A contract
   migration that raises the minimum does not run while a version row below
   the new minimum exists. The binary then starts without that migration,
   and the error names the rows that block it.
6. **The write path checks the minimum.** The write-path check of decision 3
   reads the installation row already. The same statement compares the
   minimum with the version of the writing binary. So an old instance that
   is still running when the minimum goes up, for example because an
   administrator deleted its row, cannot write after that. Its writes fail in
   their own transaction, like a namespace fence. This makes step 5 a
   precondition for the operator, not the only protection.
7. **A test matrix.** CI runs the new schema against each of the last K
   release binaries. Each old binary runs the e2e steps of the real CLIs
   while a new binary serves on the same database.

What this gives: during a rolling deploy, binaries from release R−K to R can
run together on one schema. A rolling deploy can skip up to K−1 releases. A
stopped upgrade can skip any number (part 1).

Cost of each step:

| Step | Cost |
| --- | --- |
| Expand only | Each shape change becomes two releases or more. Code writes both shapes for K releases. |
| Delayed contract | A list of pending contracts, each with the release that may run it. A review rule. |
| Minimum version | One column, a check at start, and the change to ADR 33 decision 1. |
| Version rows | One table, one write at start and one delete at stop, one API route to list and one to delete, with an action. |
| Contract precondition | One query in the migration runner. |
| Write-path minimum | One comparison in a statement that decision 3 adds anyway. |
| Test matrix | K builds and K mixed e2e runs per dialect for each release. The cost grows linearly with K. |

### Why this must be decided before the first tag

A released binary keeps its refusal rule. If `v0.1.0` refuses any newer
schema (ADR 33 decision 1 as it stands), then no `v0.1.0` instance can serve
next to a `v0.2.0` instance, whatever `v0.2.0` does. ADR 33 decision 2 uses
the same argument for the refusal itself. So steps 3 and 6 must be in the
first tag. K itself is a number in the schema, and a later release can
change it.

### Recommendation for K

K = 2. Three releases (R−2, R−1, R) can run together on one schema, so a
rolling deploy can skip one release. A stopped upgrade can skip any number.
K = 1 (N−1) is the minimum for a rolling deploy at all. Each step above K = 2
adds one build and one mixed e2e run per dialect to every release, and keeps
dual-shape code for one more release. The project has no release cadence
yet. When it has one, K can grow if operators need it.

## Considered and rejected

These options were in earlier drafts of this RFC. The owner decided against
them on 2026-10-16. They are kept here so that later reviews do not propose
them again.

- **One lease per instance (L2, an instance registry).** Each instance holds
  one registration row with a lease, and a claim records the owner's
  registration ID (review A1, A5, A6). Rejected: the owner chose a lease per
  job (decision 2). A registry adds a table, a renewal loop on every
  instance and a rule for an instance that loses its lease, only to answer
  "is the owner live". A lease per job answers it with one write per running
  operation, and the number of running operations is small.
- **Confirmation through the registry** (review B3). Each instance writes the
  maintenance epoch that its gate enforces into its registration row, and the
  restore waits for every live row. Rejected with the registry, and the
  write-path check (decision 3) needs no wait.
- **DBOS Transact Go behind the port.** A throwaway spike (draft #693,
  commit d0d9160, not for merge) ran eight cases on both stores against our
  adapter (436 lines) and a DBOS v1.7.0 adapter (269 lines). Rejected
  (decision 1) for these reasons from the spike and the source:
  - Without Conductor, a commercial product, a process recovers only the
    workflows of its own executor ID. With unique IDs, the work of a killed
    instance stays `PENDING`. With one shared ID, instances recover each
    other's live work (#667). The takeover case failed on both stores.
  - The default application version is a hash of the binary, and recovery
    takes only workflows of its own version, so a rolling deploy strands
    work.
  - A step commits atomically with its checkpoint only on the DBOS pool, and
    `Shutdown` closes that pool. Our sqlc SQLite code cannot run in its
    transaction type.
  - On SQLite, a second DBOS instance blocked with no limit while a step of
    the first one stalled. On SQLite its tables land in our database file.
  - It adds about 123 migrations of its own, plpgsql functions and a trigger,
    and two modules that the binary does not link today.
  - Our adapter passed every case on PostgreSQL. Its one failure on SQLite is
    a property of SQLite (see the suite).
- **River** (MPL-2.0). A strong PostgreSQL queue, but its SQLite driver is
  "in early testing", durable periodic jobs are a paid extension, and its
  migrations would have to move into our migrations.
- **go-workflows** (MIT). No start or completion inside our transaction,
  constructors that panic, unqualified table names and one maintainer.
- **A replay engine with its own cluster** (Temporal and similar). Not
  embeddable, and replay engines put the highest cost on workflow code
  (research, part 2).
- **Do nothing.** #667 and #668 stay data loss.
- **A row lock held while the work runs** (`SELECT ... FOR UPDATE SKIP
  LOCKED` for the whole job). It pins a pool connection, holds back vacuum,
  fails behind a transaction-mode pooler, survives a dead host for the TCP
  keepalive time, and can wait on the restore swap in a cycle that the
  deadlock detector cannot see (review A5, research part 5). `SKIP LOCKED`
  stays only in the short claim transaction.
- **The sign-in failure budget per instance, with the factor in the guide.**
  Rejected: the owner chose a shared counter (decision 7).
- **Squash the migrations only at the first tag** (review A12). Rejected: the
  owner chose to collapse them now (decision 5).

## Rollout

1. The two-handle store factory and `internal/servertest`, with the #667 and
   #668 reproductions on them.
2. The port and the conformance suite.
3. Our adapter on both stores.
4. The control-table schema and the write-path installation check, with the
   ADR that supersedes ADR 48 decisions 5 to 8.
5. Backup, restore and the chain migration as operations; the start-up
   recovery steps go away.
6. The tickers become schedules.
7. The shared sign-in failure counter.
8. Deletion of the pre-release code and the baseline migration per dialect.
   This can go first, because it shrinks the code that the later steps
   change.

SQLite runs the same port and the same suite with one instance. A guard
refuses a second instance on one SQLite file.

## Open questions

1. Schedules: does a missed tick run once after a random delay at start-up,
   claimed by one instance (the proposal for question 8)?
2. Rolling deploys: expand and contract with a support window of K = 2,
   a minimum binary version in the schema, version rows written at start-up
   and a write-path check of the minimum (the proposal for question 9)? If
   yes, it supersedes ADR 33 decision 1 and must land before the first tag.
3. ADR 48 decisions 1 to 3 and 9 to 11 define an instance registry with a
   lease. Decision 2 above says that there is no instance registry, and
   decisions 5 to 8 were the only users of the registry. Are decisions 1 to
   3 and 9 to 11 superseded too? Decision 4 (the version check at start)
   moves to question 2.

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

Questions 1 to 7 decided on 2026-10-16. ADR 72 records them, proposed in
#699. Questions 8 and 9 are open.
