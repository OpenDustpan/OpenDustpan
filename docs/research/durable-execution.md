# Research: durable execution for several instances on one database

Question (RFC 36, draft pull request 671): OpenDustpan must scale
horizontally, with several instances on one database. The server has work
that must finish after a crash or a restart: backup, restore, the journal
build, periodic sweeps and audit shipping. The project owner has not decided
between two ways to run that work:

1. An embedded durable execution library, for example DBOS Transact for Go,
   behind a port.
2. A small framework of our own behind a port, with a conformance suite.

This note answers four questions:

1. Where do the ideas of durable execution come from, and what do they
   assume?
2. Which engines exist, how do they run, and what do they cost?
3. What fails in systems of this kind, and where did teams get it wrong?
4. What follows for OpenDustpan?

Date: 2026-10-16

This note gives facts and one recommendation. It decides nothing. The owner
decides in RFC 36 (ADR 15). Parts 1 to 5 report what the sources say. Part 6
is analysis, and it is marked as analysis.

Method: each claim cites a primary source: a paper, the official
documentation, or source code at a named tag or commit. Source code was read
from shallow clones on 2026-10-16. "Unverified" marks a claim that this note
could not check against a primary source. Version numbers are the highest
release tag that `git ls-remote` showed on 2026-10-16.

## Summary

- **The foundations are old and agree.** Sagas (1987) split a long
  transaction into steps with compensations. Helland (2007, 2009) argues that
  at scale, messages arrive at least once, so each step must be idempotent.
  Gray and Cheriton (1989) and Chubby (2006) show that a lease is safe only
  under a bound on clock drift. Chubby and Kleppmann (2016) show that a lease
  alone does not protect data: the resource must also check a fencing token
  (a "sequencer" in Chubby).
- **Four paradigms exist.** (a) Deterministic replay of an event history:
  Amazon SWF, the Durable Task Framework, Cadence, Temporal, go-workflows.
  (b) A checkpoint for each step, with re-execution that skips completed
  steps: DBOS, Absurd, Restate (a journal in a log). (c) A job queue with
  idempotent handlers: River, Graphile Worker, Oban, Que, Solid Queue.
  (d) A state machine in domain rows, with a lease on the row. OpenDustpan
  already uses (d) for updates, workspace locks and audit sinks.
- **Replay engines cost the most.** The code must be deterministic. A code
  change to a running workflow needs patching or versioning. By default,
  Temporal terminates a workflow whose history passes 51,200 events or
  50 MB. Temporal and Cadence each need a separate server cluster.
- **DBOS Transact for Go fits the shape, with three gaps.** It is an MIT
  library (v1.7.0, 2026-10-15) that runs on PostgreSQL, CockroachDB and
  SQLite, in the process, with one database write for each step. The gaps:
  (1) without the proprietary DBOS Conductor, a process recovers only the
  workflows of its own executor ID at start, so the workflows of a crashed
  instance with a unique ID stay pending. (2) By default, recovery runs only
  on a binary with the same application version, so a rolling deploy strands
  the work of the old version. (3) Its system tables, migrations and SQL sit
  outside our per-dialect store, our migrations and our backup format.
- **Most failures in hand-rolled systems are known.** A takeover runs a job
  twice (River, Graphile Worker). A paused holder writes after its lease ends
  (Kleppmann reports that HBase had this problem). A schedule fires twice
  (Kubernetes CronJob). A job runs before its transaction commits (Brandur Leach). A long transaction
  stops vacuum and slows the queue table (Brandur Leach, 2015). A clock goes
  backwards (Cloudflare, 2017).
- **Recommendation (analysis).** Our own small framework behind a port, with
  the paradigm that the server already uses: state in domain rows, a job
  lease with a fencing epoch that the store checks in the transaction of each
  write, start keys, database-clock expiry and idempotent steps. The work is
  a few coarse administrative jobs, not many fine-grained workflows. DBOS
  stays a valid option behind the same port if the owner accepts the three
  gaps above, and the conformance suite in part 6 applies to both.

## Part 1. Foundations

### Sagas (1987)

Garcia-Molina and Salem define a saga for long-lived transactions (LLTs). A
long transaction holds locks for a long time and delays shorter
transactions. A saga is a sequence of transactions T1 to Tn that "should be
executed as a (non-atomic) unit": "any partial executions of the saga are
undesirable, and if they occur, must be compensated for". Each Ti has a
compensating transaction Ci that "undoes, from a semantic point of view, any
of the actions performed by Ti, but does not necessarily return the database
to the state that existed when the execution of Ti began" [sagas]. A saga
gives either T1 ... Tn, or T1 ... Tj followed by Cj ... C1. It does not give
isolation: other transactions can see the results of Ti before Ci runs.

For OpenDustpan, a restore is a saga in this sense: ADR 38 takes a backup
before the apply (decision 8), so that the operator can undo the restore.

### Life beyond Distributed Transactions (2007)

Helland's position paper (CIDR 2007, written at Amazon) argues that large
applications do not use distributed transactions. It names entities (data
with a unique key inside one scope of serializability) and activities (the
state that one entity keeps for each partner). Its section "Most
Applications Use 'At-Least-Once' Messaging" describes the dilemma: "The
message is consumed but not yet acknowledged. The database is updated and
then the message is acknowledged. In a failure, this is restarted and the
message is processed again." So the processing must be idempotent: "The
processing of a message is idempotent if a subsequent execution of the
processing does not perform a substantive change to the entity" [helland07].

### Building on Quicksand (2009)

Helland and Campbell (CIDR 2009, at Microsoft) generalize this: "Fault
tolerant algorithms comprise a set of idempotent sub-algorithms. Between
these idempotent sub-algorithms, state is sent across the failure boundaries
of the unreliable components." A backup "uses the last known state and
drives forward with a retry of the idempotent sub-algorithm." Requests carry
a "uniquifier" so that a retry is associated with the first request. They
also note: "Transactions can make it EASIER to build idempotent
sub-algorithms. [...] All that remains is to ensure the work is either not
started more than once or that a second attempt will detect a successful
first attempt and be innocuous" [quicksand].

This is the model of every engine in part 2: a durable record between steps,
and steps that are safe to retry.

### The transactional outbox

Richardson's pattern catalog states the problem: "How to atomically update
the database and send messages to a message broker?" The service writes the
message to an outbox table in the same transaction as the business change. A
separate relay sends the stored messages. The relay "might publish a message
more than once", so consumers must be idempotent [outbox]. Brandur Leach
describes the same idea for job queues as a "transactionally staged job
drain" (2017): a transaction inserts jobs into a staging table, so a
rollback discards them and a worker never sees a job before its data
commits [job-drain].

OpenDustpan's audit shipping (ADR 60, decision 13) already has this shape:
the SQL table is the record, a shipper keeps a cursor for each sink, and "a
sink can receive an entry twice".

### Leases, fencing tokens and clocks

- **Leases (1989).** Gray and Cheriton propose leases for cache consistency.
  "Leases depend on well-behaved clocks." A server clock that runs fast, or
  a client clock that runs slow, can break consistency. The minimum
  requirement is "that clocks have a known bounded drift" [leases].
- **Chubby (2006).** Burrows describes the problem: a lock holder "may issue
  a request R, but then fail. Another process may acquire L and perform some
  action before R arrives at its destination. If R later arrives, it may be
  acted on without the protection of L." Chubby's answer is a sequencer that
  holds the lock name, the mode and "the lock generation number". The client
  passes it to servers, and "the recipient server is expected to test whether
  the sequencer is still valid [...] if not, it should reject the request."
  For servers that cannot check sequencers, Chubby has a "lock-delay" (up to
  one minute) after a holder fails, which it calls "imperfect". The client
  lease timeout is "a conservative approximation of the master's lease
  timeout", and Chubby requires "that the server's clock advance no faster
  than a known constant factor faster than the client's" [chubby].
- **Kleppmann (2016).** A lease with a timeout alone is unsafe "if the GC
  pause lasts longer than the lease expiry period", and a write "may reach
  the storage server a minute later" because of network delay. Kleppmann
  reports that HBase had this problem. This note did not read a primary
  HBase source. A fencing token is "simply a number that
  increases" on each acquisition. The storage server remembers the highest
  token that it processed and rejects a write with a lower one. He separates
  locks for efficiency (a failure costs duplicate work) from locks for
  correctness (a failure corrupts data). Redlock "assumes that delays, pauses
  and drift are all small relative to the time-to-live of a lock" and cannot
  make fencing tokens [kleppmann].

OpenDustpan already has two parts of this. The namespace fence (ADR 4) is a
fencing token: the failover version that a write read, checked by the store
in the transaction of the write. The audit sink lease computes its expiry
from the database clock (`clock_timestamp()` in
`pkg/store/postgres/queries.sql`, `AcquireAuditSinkLease`), "so a wrong clock
on one instance cannot hold every lease".

### Clocks in practice

- PostgreSQL: `now()` and `CURRENT_TIMESTAMP` return the start time of the
  current transaction, and "their values do not change during the
  transaction". `clock_timestamp()` "returns the actual current time, and
  therefore its value changes even within a single SQL statement" [pg-time].
  A lease check with `now()` inside a long transaction compares against an
  old time.
- Go: `time.Now` holds a wall clock reading and a monotonic reading.
  Comparisons and `Sub` use the monotonic reading. Serialization drops it,
  because it "has no meaning outside the current process" [go-time]. So a
  lease end that one process stores and another process reads is a wall
  clock value.
- Cloudflare, 1 January 2017: the leap second made time appear to go
  backwards. A measured duration became negative and `rand.Int63n` panicked.
  "The root cause [...] was the belief that time cannot go backwards." About
  0.2% of DNS queries failed at peak, for up to 6 hours 45 minutes
  [cloudflare].

## Part 2. Workflow engines with deterministic replay

### History

- **Amazon SWF.** AWS announced Simple Workflow Service on 2012-02-21
  [swf-launch]. A program reads the task state from SWF and starts the next
  tasks. SWF "maintains an application's execution state durably". AWS now
  writes: "For most use cases, we recommend considering AWS Step Functions"
  [swf].
- **People.** Temporal's about page says that Maxim Fateev and Samar Abbas
  "launched the first public version of Amazon's Simple Workflow Service",
  that "Samar created the Azure Durable Task Framework at Microsoft", and
  that in 2015 they "reunited at Uber to co-create Cadence" [temporal-about].
- **Durable Task Framework and Durable Functions.** The framework lets users
  "write long running persistent workflows in C# using the async/await
  capabilities". It is Apache-2.0 and "community-maintained", with storage
  providers for Azure Storage, Service Fabric, Netherite and SQL Server
  [durabletask]. The README recommends Durable Functions, which has official
  Microsoft support, for serverless orchestration on Azure [durabletask].
- **Cadence.** Uber open-sourced Cadence in 2017 [cadence-uber]. It is
  Apache-2.0 (latest tag v1.4.2), and its backend is "multiple services, a
  database (Cassandra/MySQL/PostgreSQL) and optionally Kafka+Elasticsearch"
  [cadence].
- **Temporal.** "Temporal [...] originated as a fork of Uber's Cadence." The
  server is MIT (latest stable tag v1.32.1) [temporal]. It runs as a separate
  cluster. RFC 7 already uses its one-binary, many-services layout as a
  model.

### The replay model

All of these engines record an event history for each workflow and rebuild
the state of the workflow function by replaying it:

- Durable Functions: orchestrators "use event sourcing to ensure reliable
  execution and to maintain local variable state". An orchestrator "replays
  multiple times, and it must produce the same result each time". It must
  not read the current time, make GUIDs, do I/O or start threads, and must
  use the context's replay-safe equivalents. The Go SDK forbids goroutines,
  channels, `select`, `time.Sleep` and map iteration in orchestrators. "Code
  changes that affect running orchestrations can break replay behavior"
  [df-constraints].
- Cadence: after a host failure, "Host B needs to redo all its history until
  the moment of failure." Replay compares the decisions that the code makes
  with the history. Missing, extra or different decisions raise a
  non-deterministic error. Reordering operations or changing timers causes
  it [cadence-nde].
- Temporal: the code must "make the same Workflow API calls in the same
  sequence, given the same input". A change that adds, removes or reorders
  commands needs versioning. Temporal offers patching in code and Worker
  Versioning (old workers run old code) [temporal-def].
- go-workflows (MIT, v1.4.2): the same model in a Go library, with SQLite,
  MySQL, PostgreSQL and Redis backends. Workflows "must not use any of Go's
  non-deterministic features (`select`, iteration over a `map`, etc.)"
  [go-workflows].

### Costs

- **Determinism.** Every engine above restricts the language in workflow
  code. A Go developer cannot use the standard concurrency tools there.
- **Versioning.** A code change to a running workflow needs patching or a
  version split. This applies to every deploy while workflows run.
- **History size.** By default, Temporal warns at 10,240 events or 10 MB and
  terminates the workflow at 51,200 events or 50 MB. A payload is limited to
  2 MB. The history limit "is non-configurable for Temporal Cloud". A
  self-hosted server can change it with `HistoryCountLimitError` and
  `HistorySizeLimitError` [temporal-limits] [temporal-defaults]. Step
  Functions Standard fails an
  execution at 25,000 history events and limits a state's input or output to
  256 KiB [sfn-quotas]. Long-running workflows use "continue as new" or a new
  execution.
- **Operations.** Cadence and Temporal are server clusters with their own
  database. That is a second stateful system next to OpenDustpan's database.

### AWS Step Functions

Step Functions describes a state machine in a JSON language (ASL), not in
code, so the determinism rule does not apply to user code. Standard
workflows run up to one year and "follow an exactly-once model, where your
tasks and states are never run more than once, unless you have specified
`Retry` behavior". A start with the name of a running execution is
idempotent. Express workflows run up to five minutes, at least once
(asynchronous) or at most once (synchronous), and "Idempotency is not
automatically managed" [sfn-types]. It is a hosted AWS service only.

## Part 3. Database-centric durable execution and queues

### DBOS

**Research.** DBOS started as a research project on an operating system on a
distributed transactional DBMS (Skiadopoulos et al., PVLDB 15(1), 2022,
16 authors including Stonebraker and Zaharia) [dbos-vldb]. "DBOS: three
years later" (Li, Kraft, Kozyrakis, Zaharia, Stonebraker, VLDB Journal 2025)
reports the move to "a venture-capital-backed startup" and the changes for
commercial use [dbos-3y].

**The model.** DBOS stores "every workflow input and step output" in a
system database: "one database write per step", plus two for each workflow.
At recovery, DBOS calls the workflow again with its stored inputs. Before
each step it checks for a stored output: "If there is a checkpoint, the step
returns the checkpointed output instead of executing." The first step with
no checkpoint runs normally [dbos-arch]. So DBOS does not replay a full
event history. It re-executes the workflow function and skips completed
steps. The workflow function must still be deterministic in the steps it
calls and their order [dbos-arch].

**Guarantees.** Steps run "at least once: if a process crashes after a
step's side effects but before the step is checkpointed, the step
re-executes on recovery" [dbos-steps]. A datasource transaction commits the
application writes and the DBOS record together, so it runs exactly once
[dbos-tx]. A workflow ID is an idempotency key. A scheduled run has the ID
`sched-<name>-<RFC3339 time>`, and the scheduler skips a tick whose workflow
already exists, so "each scheduled invocation occurs exactly once even when
multiple application instances share the same schedule" [dbos-sched]
(`dbos/scheduler.go` at v1.7.0). Cancellation takes effect at "the start of
its next durable operation" [dbos-cancel].

**DBOS Transact for Go, v1.7.0 (commit `339705771d7e`, 2026-10-15).** MIT.
A library in the process. It needs no separate server. `dbos/internal/sysdb/dialect.go`
has three dialects: `postgres`, `cockroach` and `sqlite`. The docs call
SQLite "useful for local development, testing, and single-node deployments",
behind a blank import of the driver [dbos-golang-config]. The `go.mod`
requires `pgx/v5`, `modernc.org/sqlite`, `robfig/cron/v3`, `spf13/cobra`,
`spf13/viper`, `stretchr/testify`, `google/uuid` and `goleak`. Only
`cmd/dbos/` imports `cobra` and `viper`, and only `_test.go` files import
`testify` and `goleak`. These modules go into `go.sum`, but a binary that
imports the `dbos` package does not link them. The packages that `dbos`
links are `google/uuid`, `jackc/pgx/v5` (`pgconn`, `pgxpool`),
`jackc/pgerrcode` and `robfig/cron/v3`, plus `modernc.org/sqlite` through
`dbos/driver/sqlite` (imports of the non-test files at v1.7.0). Queues
dequeue with `FOR UPDATE SKIP LOCKED` where the dialect has it.

**Recovery with several instances.** This is the part that matters most for
RFC 36:

- Each workflow row carries the executor ID of the process that started it.
  The default executor ID is `"local"`, or the value of `DBOS__VMID`
  (`dbos/dbos.go`, lines 165 to 175).
- At `Launch`, a process without Conductor calls
  `recoverPendingWorkflows(c, []string{c.executorID})`
  (`dbos/dbos.go`, line 904). `ReenqueueForRecovery` re-enqueues every
  `PENDING` workflow with that executor ID and the same application version.
  The query has no liveness check and no lease
  (`dbos/internal/sysdb/system_database.go`, line 5345).
- The docs: "When self-hosting in a distributed setting without Conductor
  [...] You should assign each executor [...] an executor ID [...] When an
  application with an executor ID restarts, it only recovers pending
  workflows assigned to that executor ID" [dbos-recovery].
- So two instances that share an executor ID recover each other's live
  workflows at each start. That is the defect of issue 667. Two instances
  with unique IDs never recover each other's work: the work of a crashed
  instance waits until a process with the same ID starts.
- DBOS Conductor fills the gap: it marks a disconnected executor `DEAD`
  after 60 seconds by default and "signals another executor to recover its
  workflows" [dbos-conductor]. Self-hosted Conductor "is released under a
  proprietary license", and production use needs a license key. A
  development key allows one executor per application [dbos-conductor-k8s].
  The Go client connects through the commercial module
  `go.dbos.dev/dbos-enterprise-go` (`dbos/enterprise.go`).
- DBOS fences its own records. Each execution holds an owner token
  (`owner_xid`). `checkOwner` locks the workflow row and fails with a
  conflicting-ID error unless the token still owns it, and a step record
  (`RecordOperationResult`) or a status change applies only for the owner.
  Recovery and cancellation clear the token, so a zombie execution cannot
  record a step after a hand-off (`dbos/internal/sysdb/system_database.go`,
  lines 3049 to 3065). Side effects inside the step, outside these records,
  are not fenced.
- The package `dbos/internals` is public. `internals.RuntimeOf(ctx)` returns
  a `Runtime` whose method `RecoverPendingWorkflows(executorIDs)` the
  commercial module calls (`dbos/internals/internals.go`). An application can
  call it with its own liveness signal. This note did not test that path.
- Versioning: by default the application version is a SHA-256 of the binary
  and the application name (`computeApplicationVersion`). DBOS "only
  continues workflow execution with the same application version that
  started the workflow" [dbos-upgrade]. After a rolling deploy, a pending
  workflow of the old version does not recover on a new binary, unless the
  operator sets a stable version and uses patching.

**Scale and users.** DBOS states that one PostgreSQL database sustains more
than 40,000 workflows or steps per second [dbos-arch]. Its customer page
names, among others, Notion, Supabase, Walmart, Roche and SeatGeek
[dbos-customers].

### Restate

Restate is a server, "all the functionality in a single binary". Each
partition processor "tails the log", calls the handler code, and keeps state
in embedded RocksDB. The log (Bifrost) is replicated, and snapshots go to S3.
Each step is committed to the log before it counts as done. On a retry the
server "attaches the full journal so far", so a completed step "won't be
re-executed" [restate-arch]. SDKs: TypeScript, Java and Kotlin, Python, Go,
Rust [restate]. License: Business Source License 1.1 with a change to
Apache-2.0 four years after each release (latest tag v1.8.0) [restate].
It needs no external database, but it is a second stateful system.

### Inngest and Hatchet

- **Inngest** (latest tag v1.46.0). The self-hosted server is "a single
  binary that includes all Inngest services". It uses SQLite by default, and
  PostgreSQL or Redis in production [inngest-self]. License: Server Side
  Public License 1.0 with an "Apache 2.0 Future License" after three years
  [inngest]. That the server calls the application's functions over HTTP is
  unverified in the page that this note read.
- **Hatchet** (MIT, latest tag v0.112.9). A self-hosted control plane has an
  API server, an engine (gRPC), PostgreSQL "for storing workflow state and
  metadata" and an optional RabbitMQ [hatchet-self].

Both are separate servers that an OpenDustpan operator would have to run.

### PostgreSQL job queues

| Library | Language | License | Databases | Crash rescue | Schedules |
| --- | --- | --- | --- | --- | --- |
| River v0.49.0 | Go | MPL-2.0 | PostgreSQL. SQLite through the separate module `riversqlite`, which the changelog calls preliminary | A job running longer than `RescueStuckJobsAfter` (default 1 hour) is retried. The docs warn: "this can result in repeat or duplicate execution of a job that is not actually stuck" (`client.go`). | Periodic jobs need leader election (`client.go`, line 612). |
| Graphile Worker v0.18.0 | Node.js | MIT | PostgreSQL | After a hard kill, jobs "remain locked for at least 4 hours". Workers sweep every 8 to 10 minutes. | A `known_crontabs` table makes the first worker that queues a tick win. |
| Oban v2.24.2 | Elixir | Apache-2.0 | PostgreSQL, MySQL, SQLite | Jobs left after the shutdown grace period "may be rescued later". | Not checked. |
| Que v2.4.1 | Ruby | MIT | PostgreSQL | Advisory locks. | Not checked. |
| Solid Queue v1.7.0 | Ruby | MIT | PostgreSQL, MySQL, SQLite | Process heartbeats every 60 s. A process is dead after 5 minutes. Its claimed jobs are marked failed with `ProcessPrunedError`. A stuck job in a live process stays claimed. | A unique index on `task_key` and `run_at` in `solid_queue_recurring_executions`. |

Sources: [river], [river-sqlite], [graphile-errors], [graphile-cron], [oban], [que],
[solid-queue]. Graphile Worker states the guarantee of the class: "each job
will execute at least once. Most jobs will execute exactly once"
[graphile-index]. Solid Queue runs "20 million jobs per day for HEY alone at
37signals" [rails8]. The Que README warns that its job table "is susceptible
to bloat and slowness if Postgres isn't able to clean it up" [que].

### Recent PostgreSQL-only designs: Absurd

Absurd (Apache-2.0, tag 0.5.0, main at commit `5b97022ac6d1`, 2026-08-04) is
"the simplest durable execution workflow system you can think of. It's
entirely based on Postgres and nothing else." It is one SQL file of
functions with thin SDKs. The Go SDK is an "experimental bootstrap"
[absurd]. In `sql/absurd.sql`:

- `absurd.current_time()` is `clock_timestamp()`, so every expiry uses the
  database clock.
- `claim_task` gives a run a claim that ends after `claim_timeout` (default
  30 seconds). Before it claims, it fails runs whose claim ended, with
  `FOR UPDATE SKIP LOCKED`, and records `$ClaimTimeout`.
- `set_task_checkpoint_state` stores a step's state under the run ID. It
  refuses a checkpoint from a run that has failed (`AB002`), and it keeps the
  checkpoint of a later attempt over an earlier one. The attempt number acts
  as a fencing token for checkpoints.

### Comparison

| Engine | Paradigm | Run model | License | Databases | Go | Latest tag |
| --- | --- | --- | --- | --- | --- | --- |
| Temporal | Replay | Server cluster | MIT | Not checked here | SDK | v1.32.1 |
| Cadence | Replay | Server cluster | Apache-2.0 | Cassandra, MySQL, PostgreSQL | SDK | v1.4.2 |
| Durable Task Framework | Replay | Library (.NET) | Apache-2.0 | Azure Storage, SQL Server, others | durabletask-go | Not checked |
| Step Functions | State machine (ASL) | AWS service | Proprietary | Hosted | AWS SDK | n/a |
| go-workflows | Replay | Library | MIT | SQLite, MySQL, PostgreSQL, Redis | Native | v1.4.2 |
| DBOS Transact Go | Step checkpoint | Library | MIT (Conductor proprietary) | PostgreSQL, CockroachDB, SQLite | Native | v1.7.0 |
| Restate | Journal in a log | Server | BSL 1.1 | Embedded (RocksDB, log) | SDK | v1.8.0 |
| Inngest | Not verified | Server | SSPL 1.0, later Apache-2.0 | SQLite, PostgreSQL, Redis | SDK | v1.46.0 |
| Hatchet | Durable task queue | Server | MIT | PostgreSQL (+ RabbitMQ) | SDK | v0.112.9 |
| Absurd | Step checkpoint | SQL functions + SDK | Apache-2.0 | PostgreSQL | Experimental | 0.5.0 |
| River | Job queue | Library | MPL-2.0 | PostgreSQL, SQLite | Native | v0.49.0 |

Our license gate (`scripts/trivy-license-gate.py`) passes MIT, BSD,
Apache-2.0 and MPL-2.0. BSL and SSPL are source-available licenses. The
licenses research note records why OpenTofu left the BSL.

## Part 4. The paradigms compared

| | Deterministic replay | Step checkpoint | Job queue, idempotent handler | State machine in domain rows |
| --- | --- | --- | --- | --- |
| Examples | SWF, DTF, Cadence, Temporal, go-workflows | DBOS, Absurd, Restate | River, Graphile Worker, Oban, Solid Queue | OpenDustpan updates, workspace locks, audit sinks, restore rows |
| Durable record | Full event history | One row per completed step | One row per job | Domain row with a state column and a lease |
| Recovery | Replay all history, then continue | Re-run the function, skip stored steps | Retry the whole job | Resume from the state column |
| Code rule | Deterministic workflow code, replay-safe APIs | Deterministic order of steps. Steps idempotent | Handler idempotent | Each transition idempotent and fenced |
| Code change while running | Patching or versioning | Patching or versioning (DBOS) | Free, if the job input stays readable | Free, if states stay readable |
| Side effect guarantee | Activity at least once | Step at least once. A transaction step in the same database exactly once | At least once | At least once, unless the effect is in the same transaction |
| Limits | History size (Temporal default: terminate at 51,200 events / 50 MB) | Rows per step. Output size | Queue table churn | None beyond the domain |
| Operations | Separate cluster | Library tables in our database | Library tables in our database | Our own tables |

The failure modes that each paradigm must handle:

- **Zombie worker.** A paused or partitioned worker continues after another
  worker takes its work. A lease alone does not stop it (Kleppmann). DBOS
  refuses a step record from an execution that no longer holds the owner
  token. Absurd refuses a checkpoint from a failed run. Replay engines
  compare each command with the history. How they refuse a stale worker was
  not checked for this note. A side effect outside the database still
  happens twice. River and Graphile Worker say directly that a takeover can
  run a job twice.
- **Poison message.** A job that always fails loops forever without a limit.
  SQS moves a message to a dead-letter queue after `maxReceiveCount`
  receives [sqs-dlq]. River discards a job after its last attempt. DBOS has
  the status `MAX_RECOVERY_ATTEMPTS_EXCEEDED` (`cmd/dbos/workflow.go`).
- **Schedule double-fire.** A Kubernetes CronJob creates a job "approximately
  once per execution time [...] two Jobs might be created, or no Job might be
  created [...] the Jobs that you define should be idempotent" [k8s-cron].
  The database designs remove the duplicate with a unique key for each tick:
  DBOS (workflow ID), Solid Queue (unique index), Graphile Worker
  (`known_crontabs`). A unique key does not remove a missed tick. Graphile
  Worker has an explicit backfill option, and Kubernetes skips a job after
  more than 100 missed schedules.
- **Side effect before commit.** A job that a transaction enqueues to a
  separate system can run before the commit, or after a rollback
  [job-drain]. A queue in the same database, written in the same
  transaction, removes this case.
- **Long transactions.** An old open transaction stops vacuum. In Brandur
  Leach's test, dead rows in the job table approached 100,000, a manual
  vacuum reported 247,311 dead row versions that it could not remove, lock time
  rose from under 10 ms to over 100 ms, and the queue grew to about 60,000
  jobs in one hour [mvcc-queue].

Operational cost, in order: a separate cluster (Temporal, Cadence, Restate,
Inngest, Hatchet) costs a second stateful system, its upgrades and its
backups. A library with its own tables (DBOS, River, Absurd) costs a second
schema in our database, with its own migrations. Domain rows cost only our
own code and tests.

## Part 5. Patterns and pitfalls for a hand-rolled system

- **Claim with `FOR UPDATE SKIP LOCKED`.** PostgreSQL documents the use:
  "Skipping locked rows provides an inconsistent view of the data, so this
  is not suitable for general purpose work, but can be used to avoid lock
  contention with multiple consumers accessing a queue-like table"
  [pg-select]. Keep the claim transaction short, and commit the claim before
  the work starts. A transaction held open for the whole job causes the
  vacuum problem above.
- **Claim token and fencing.** Give each claim a value that grows: an
  attempt number or an epoch. Each write of the job checks it in its own
  transaction, as ADR 4 does for namespaces. "Stop at the next safe point"
  is enough for efficiency. For correctness the store must refuse the late
  write (Kleppmann, Chubby).
- **Expiry from the database clock.** Compute and compare lease ends with
  `clock_timestamp()`, not with the instance clock and not with `now()`.
  OpenDustpan's audit sink lease and Absurd already do this. ADR 48
  decision 7 adds a clock-skew bound where an instance must reason about
  another instance's lease.
- **Advisory locks.** A session-level advisory lock is "held until
  explicitly released or the session ends" and does not honor transaction
  semantics [pg-locks]. With a connection pool, the job must keep one
  connection for the whole run. RFC 36 lists the same cost, and SQLite has
  no equivalent.
- **Exactly-once start keys.** A start with a key inserts one row under a
  unique constraint, with `ON CONFLICT DO NOTHING` (ADR 3), and returns the
  existing run when the key exists. Step Functions Standard and DBOS work
  this way. Helland calls the key a "uniquifier".
- **Cron in the database.** Derive the run key from the schedule name and
  the tick time. Insert the run and the tick in one transaction. Decide the
  policy for missed ticks explicitly (skip, run once, or backfill a bounded
  window).
- **Cancellation.** A cancel is a state change in the row. The worker reads
  it at the next step, as DBOS does. A canceled run must not move to
  `succeeded` later.
- **Retries and backoff.** Retries are "selfish" and can "make the overload
  worse". Use exponential backoff with a cap and jitter, retry at one layer
  only, and retry side effects only through idempotent APIs. Three retries
  at each of five layers multiply load on the bottom layer 243 times
  [aws-retries].
- **Idempotency keys for external effects.** Brandur Leach's Stripe design
  splits a request into "atomic phases" between "foreign state mutations"
  and stores a recovery point after each phase [idem-keys]. For OpenDustpan
  the external effects are blob writes and audit sink deliveries. Blobs are
  write-once (ADR 38 decision 6), so a retry of a blob write is safe when
  the key is deterministic.
- **Observability.** Each design in part 3 records state in rows that an
  operator can query: workflow status in DBOS, job state in River, process
  heartbeats in Solid Queue. RFC 36 open question 7 asks the same for job
  leases.

## Part 6. Implications for OpenDustpan (analysis)

This part is analysis by the author of this note. It is not a decision.

### The workloads

| Work | Shape today | Duration | Count |
| --- | --- | --- | --- |
| Backup | Row with a state. In-process slot (issue 669) | Minutes to hours | Few per day |
| Restore | Row with a state (ADR 38 decision 9). A saga with an undo backup | Minutes to hours | Rare |
| Journal build | Inside the `complete` request (ADR 43 decision 9.5) | Bounded by the CLI wait | One per journaled update |
| Retention sweep, chain check, seal | Hourly or at start, on every instance (issue 670) | Seconds to minutes | One per interval |
| Audit shipping | Lease per sink, cursor in the database (ADR 60) | Continuous | One per sink |

These are a few coarse jobs. Each already has a domain row or a cursor, and
each writes through store adapters that check the namespace fence. None
needs many small workflows, durable sleep for days, or messages between
workflows. Three need exactly one runner, and two (backup, restore) need
recovery when the runner dies. That is paradigm (d), with parts of (c): a
state machine in domain rows, a job lease, and idempotent steps.

### What our port must give, and its conformance suite

Each test comes from a failure mode in parts 1 to 5. Each runs on both
stores. On SQLite with one instance where the case needs two.

1. **One holder.** Two instances claim one job at the same time. Exactly one
   claim succeeds. (Exclusion.)
2. **Takeover only after expiry.** A claim on a job with a live lease fails.
   a claim after the lease end succeeds. (Leases.)
3. **Database clock.** An instance whose clock is one hour ahead or behind
   cannot take or keep a lease early or late. Use an injected instance clock
   in the test. (Gray and Cheriton, the audit sink lease.)
4. **Long transaction.** An expiry check inside a transaction that started
   before the lease end still sees the lease as ended. (`now()` against
   `clock_timestamp()`.)
5. **Fencing.** Holder A loses the lease, holder B takes it, then A tries to
   renew, write a step or complete. Each attempt fails with a domain
   sentinel, in the same transaction as the write. (Chubby sequencer,
   Kleppmann.)
6. **Release only by the holder.** A release by a non-holder changes nothing.
7. **Crash recovery does not touch live work.** A start fails or
   re-enqueues only runs whose holder lease ended. A run of a live instance
   stays running, and no fence moves. (Issue 667, and the DBOS `"local"`
   executor ID case.)
8. **Recovery of a dead instance's work.** A run whose holder lease ended is
   claimed by another instance within one lease length, with no operator
   action and no instance with the old identity. (The DBOS gap without
   Conductor.)
9. **Recovery across versions.** A run that an older server version started
   is recovered by a newer version, or fails with an error that names the
   version. It never waits forever. (The DBOS application version rule.)
10. **Start key.** Two starts with one key give one run and the same run ID.
    (Uniquifier, Step Functions names.)
11. **Schedule.** N instances fire one tick. One run exists. A tick missed
    while no instance ran follows the stated policy. (Kubernetes CronJob.)
12. **Completed steps do not run again.** After a takeover, a step with a
    stored result returns that result. (DBOS, Absurd.)
13. **Same-transaction start.** A run started in the transaction of a domain
    write does not exist after a rollback, and is not visible before the
    commit. (Outbox, job drain.)
14. **Poison.** A run that fails on each attempt stops after the attempt
    limit in a terminal state with the last error. (SQS dead-letter queue.)
15. **Backoff.** The next attempt time grows, has a cap, and has jitter
    inside stated bounds. (AWS Builders' Library.)
16. **Cancellation.** A cancel is seen at the next step. A canceled run
    cannot complete as succeeded. (DBOS.)
17. **Restore.** A run that holds a lease from before a restore cannot write
    after the restore moves the fences. (ADR 38 decision 7.)
18. **Listing.** Every lease and run lists its holder, its lease end and its
    attempt. (RFC 36 open question 7.)

### DBOS behind the port, or our own adapter

Adopting DBOS Transact Go behind the port:

- For: MIT. A library. PostgreSQL, CockroachDB and SQLite, so it matches our
  two dialects. Step checkpoints without full replay. An owner token that
  fences its own records. Start keys, schedules
  with tick keys, queues with `SKIP LOCKED`, cancellation and patching
  already built and tested. A datasource transaction on the system database
  pool commits a step and its writes together, but see the pool item below.
- Against, from the sources above:
  - Without Conductor, cross-instance recovery is by executor ID only
    (tests 7 and 8 fail as shipped). Conductor is proprietary. We would
    call `internals.RuntimeOf(ctx).RecoverPendingWorkflows(...)` from our own
    liveness signal
    (the ADR 48 registry), which is an internal contract between DBOS and
    its commercial module.
  - Recovery requires the same application version by default (test 9).
    We would set a stable version and depend on patching.
  - DBOS writes its own schema with its own migrations and SQL. ADR 3 keeps
    SQL per dialect in our store adapters with sqlc, the frozen migration
    test covers only our files, and the backup format and the restore swap
    (ADR 38, ADR 42) would have to include the DBOS tables, or recovery
    after a restore would see runs from before it.
  - The workflow steps must still call our store adapters, and each write
    must still carry the namespace fence (ADR 4). The DBOS owner token
    fences the DBOS records, not our domain rows, so tests 5 and 17 stay our
    code either way.
  - DBOS closes the system database pool that the caller gives it.
    `SysDB.Shutdown` calls `Close` on the `*pgxpool.Pool` or `*sql.DB` from
    `SystemDBPool` or `SQLiteSystemDB` (`dbos/internal/sysdb/system_database.go`
    line 1317, adapters at `dbq.go` lines 126 and 257). So DBOS needs its own
    pool, separate from the store's pool. Our domain writes then cannot
    commit in the same transaction as the DBOS step record, and the atomic
    mode above is lost for them.
  - New linked modules that the server does not use today: `pgerrcode` and
    `robfig/cron`. `cobra`, `viper`, `testify` and `goleak` go into `go.sum`
    only. The project rules ask the owner before each new dependency.
- Our own adapter: one port in a tool-neutral package, one table or two
  (job leases and runs) in each store, a conformance suite, and code that
  this repository already has in three places (update leases, the audit sink
  lease, the chain migration claim). The cost is our own code for start
  keys, schedules, backoff and cancellation, and the tests above.

### Recommendation

Build our own small framework behind a port, in paradigm (d):

1. A job lease with a fencing epoch: RFC 36 option 3, plus an epoch that
   grows on each takeover. Each write of a job checks the epoch in its own
   transaction. This answers RFC 36 open question 2: a job that lost its
   lease cannot write, so it has nothing to roll back.
2. Lease ends from the database clock, as the audit sink lease does today.
3. Runs as domain rows for backup and restore, with a holder and a lease
   end (issue 667), and a start key.
4. Schedules with a tick key in a unique column.
5. The conformance suite above, written before the first adapter.

Keep the port small enough that a DBOS adapter can implement it later. If
the server later needs many fine-grained workflows (for example remote runs,
RFC 6), the same suite can judge DBOS or another engine, and tests 7, 8, 9
and 17 show what such an adapter must add.

## Sources

Accessed 2026-10-16 unless a commit is named.

- [sagas] Garcia-Molina, H., Salem, K. "Sagas." ACM SIGMOD 1987:
  <https://www.cs.cornell.edu/andru/cs711/2002fa/reading/sagas.pdf>
- [helland07] Helland, P. "Life beyond Distributed Transactions: an
  Apostate's Opinion." CIDR 2007:
  <https://www.cidrdb.org/cidr2007/papers/cidr07p15.pdf>
- [quicksand] Helland, P., Campbell, D. "Building on Quicksand." CIDR 2009:
  <https://arxiv.org/pdf/0909.1788>
- [outbox] Richardson, C. "Pattern: Transactional outbox":
  <https://microservices.io/patterns/data/transactional-outbox.html>
- [job-drain] Leach, B. "Transactionally Staged Job Drains in Postgres,"
  2017-09-20: <https://brandur.org/job-drain>
- [leases] Gray, C., Cheriton, D. "Leases: An Efficient Fault-Tolerant
  Mechanism for Distributed File Cache Consistency." SOSP 1989:
  <https://courses.cs.duke.edu/spring15/cps210/readings/leases-sosp89.pdf>
- [chubby] Burrows, M. "The Chubby lock service for loosely-coupled
  distributed systems." OSDI 2006:
  <https://research.google.com/archive/chubby-osdi06.pdf>
- [kleppmann] Kleppmann, M. "How to do distributed locking," 2016-02-08:
  <https://martin.kleppmann.com/2016/02/08/how-to-do-distributed-locking.html>
- [pg-time] PostgreSQL 18, "Date/Time Functions and Operators":
  <https://www.postgresql.org/docs/current/functions-datetime.html>
- [pg-select] PostgreSQL 18, "SELECT", the locking clause:
  <https://www.postgresql.org/docs/current/sql-select.html>
- [pg-locks] PostgreSQL 18, "Explicit Locking", advisory locks:
  <https://www.postgresql.org/docs/current/explicit-locking.html>
- [go-time] Go standard library, package `time`, "Monotonic Clocks":
  <https://pkg.go.dev/time>
- [cloudflare] Cloudflare, "How and why the leap second affected Cloudflare
  DNS," 2017-01-01:
  <https://blog.cloudflare.com/how-and-why-the-leap-second-affected-cloudflare-dns/>
- [swf-launch] AWS, "AWS Announces Amazon Simple Workflow Service,"
  2012-02-21: <https://aws.amazon.com/about-aws/whats-new/2012/02/21/aws-announces-swf/>
- [swf] AWS, "What is Amazon Simple Workflow Service?":
  <https://docs.aws.amazon.com/amazonswf/latest/developerguide/swf-welcome.html>
- [temporal-about] Temporal Technologies, "About":
  <https://temporal.io/about>
- [durabletask] Azure/durabletask, README:
  <https://github.com/Azure/durabletask>
- [df-constraints] Microsoft Learn, "Durable orchestrator code constraints"
  (git commit `d9cee98471ff`, 2026-10-12):
  <https://learn.microsoft.com/en-us/azure/azure-functions/durable/durable-functions-code-constraints>
- [cadence-uber] Uber Engineering, "Cadence overview," 2019-11-12:
  <https://www.uber.com/blog/open-source-orchestration-tool-cadence-overview/>
- [cadence] cadence-workflow/cadence, README and LICENSE, tag v1.4.2:
  <https://github.com/cadence-workflow/cadence>
- [cadence-nde] Cadence docs, "Workflow Non-deterministic errors":
  <https://cadenceworkflow.io/docs/go-client/workflow-non-deterministic-error>
- [temporal] temporalio/temporal, README and LICENSE, tag v1.32.1:
  <https://github.com/temporalio/temporal>
- [temporal-def] Temporal docs, "Workflow Definition":
  <https://docs.temporal.io/workflow-definition>
- [temporal-limits] Temporal Cloud docs, "Limits":
  <https://docs.temporal.io/cloud/limits>
- [temporal-defaults] Temporal docs, "Self-hosted defaults":
  <https://docs.temporal.io/self-hosted-guide/defaults>
- [temporal-in-use] Temporal, "In use": <https://temporal.io/in-use>
- [go-workflows] cschleiden/go-workflows, README and LICENSE, tag v1.4.2:
  <https://github.com/cschleiden/go-workflows>
- [sfn-types] AWS Step Functions, "Choosing workflow type":
  <https://docs.aws.amazon.com/step-functions/latest/dg/choosing-workflow-type.html>
- [sfn-quotas] AWS Step Functions, "Service quotas":
  <https://docs.aws.amazon.com/step-functions/latest/dg/service-quotas.html>
- [dbos-vldb] Skiadopoulos, A. et al. "DBOS: A DBMS-oriented Operating
  System." PVLDB 15(1):21-30, 2022, DOI 10.14778/3485450.3485454:
  <https://www.vldb.org/pvldb/vol15/p21-skiadopoulos.pdf>
- [dbos-3y] Li, Q., Kraft, P., Kozyrakis, C., Zaharia, M., Stonebraker, M.
  "DBOS: three years later." The VLDB Journal, 2025, DOI
  10.1007/s00778-024-00899-0:
  <https://mast.stanford.edu/pubs/dbos_three_years_later>
- [dbos-arch] DBOS docs, "Architecture" (dbos-docs commit `5c118aacd4b0`):
  <https://github.com/dbos-inc/dbos-docs/blob/5c118aacd4b07a8988c63041cf3e61482299403a/docs/architecture.md>
- [dbos-recovery] DBOS docs, "Workflow Recovery":
  <https://github.com/dbos-inc/dbos-docs/blob/5c118aacd4b07a8988c63041cf3e61482299403a/docs/production/workflow-recovery.md>
- [dbos-conductor] DBOS docs, "Distributed Recovery":
  <https://github.com/dbos-inc/dbos-docs/blob/5c118aacd4b07a8988c63041cf3e61482299403a/docs/conductor/distributed-recovery.md>
- [dbos-conductor-k8s] DBOS docs, "Hosting Conductor with Kubernetes":
  <https://github.com/dbos-inc/dbos-docs/blob/5c118aacd4b07a8988c63041cf3e61482299403a/docs/conductor/self-hosting/hosting-conductor-with-kubernetes.md>
- [dbos-golang-config] DBOS docs, Go configuration reference and Go prompting
  guide:
  <https://github.com/dbos-inc/dbos-docs/blob/5c118aacd4b07a8988c63041cf3e61482299403a/docs/golang/reference/configuration.md>,
  <https://github.com/dbos-inc/dbos-docs/blob/5c118aacd4b07a8988c63041cf3e61482299403a/docs/golang/prompting.md>
- [dbos-steps] DBOS docs, Go step tutorial:
  <https://github.com/dbos-inc/dbos-docs/blob/5c118aacd4b07a8988c63041cf3e61482299403a/docs/golang/tutorials/step-tutorial.md>
- [dbos-tx] DBOS docs, Go transaction tutorial:
  <https://github.com/dbos-inc/dbos-docs/blob/5c118aacd4b07a8988c63041cf3e61482299403a/docs/golang/tutorials/transaction-tutorial.md>
- [dbos-sched] DBOS docs, Go scheduled workflows:
  <https://github.com/dbos-inc/dbos-docs/blob/5c118aacd4b07a8988c63041cf3e61482299403a/docs/golang/tutorials/scheduled-workflows.md>
- [dbos-cancel] DBOS docs, Go workflow management:
  <https://github.com/dbos-inc/dbos-docs/blob/5c118aacd4b07a8988c63041cf3e61482299403a/docs/golang/tutorials/workflow-management.md>
- [dbos-upgrade] DBOS docs, Go upgrading workflows:
  <https://github.com/dbos-inc/dbos-docs/blob/5c118aacd4b07a8988c63041cf3e61482299403a/docs/golang/tutorials/upgrading-workflows.md>
- DBOS Transact Go source, tag v1.7.0 (commit `339705771d7e`): `go.mod`,
  `dbos/dbos.go`, `dbos/recovery.go`, `dbos/enterprise.go`,
  `dbos/internals/internals.go`, `dbos/scheduler.go`,
  `dbos/internal/sysdb/dialect.go`,
  `dbos/internal/sysdb/system_database.go`:
  <https://github.com/dbos-inc/dbos-transact-golang/tree/v1.7.0>
- [dbos-customers] DBOS, "Customer stories":
  <https://www.dbos.dev/customer-stories>
- [restate-arch] Restate docs, "Architecture":
  <https://docs.restate.dev/references/architecture>
- [restate] restatedev/restate, README and LICENSE, tag v1.8.0:
  <https://github.com/restatedev/restate>
- [inngest-self] Inngest docs, "Self-hosting":
  <https://www.inngest.com/docs/self-hosting>
- [inngest] inngest/inngest, LICENSE.md, tag v1.46.0:
  <https://github.com/inngest/inngest>
- [hatchet-self] Hatchet docs, "Self-hosting":
  <https://docs.hatchet.run/self-hosting>
- hatchet-dev/hatchet, README and LICENSE, tag v0.112.9:
  <https://github.com/hatchet-dev/hatchet>
- [river] riverqueue/river, `client.go` at tag v0.49.0 (commit
  `da5a26e21c18`): <https://github.com/riverqueue/river/tree/v0.49.0>
- [river-sqlite] riverqueue/river, `CHANGELOG.md` at tag v0.49.0, entry for
  0.23.0 (2025-06-04): "Preliminary River driver for SQLite
  (`riverdriver/riversqlite`). This driver seems to produce good results as
  judged by the test suite, but so far has minimal real world vetting." The
  driver is a separate Go module,
  `github.com/riverqueue/river/riverdriver/riversqlite`, with its own tags
  (for example `riverdriver/riversqlite/v0.49.0`):
  <https://github.com/riverqueue/river/blob/v0.49.0/CHANGELOG.md>
- [graphile-index] [graphile-errors] [graphile-cron] graphile/worker,
  `website/docs/index.md`, `error-handling.md`, `cron.md` at commit
  `4cda192c5df2`, tag v0.18.0: <https://github.com/graphile/worker>
- [oban] oban-bg/oban, README and LICENSE.txt, tag v2.24.2:
  <https://github.com/oban-bg/oban>
- [que] que-rb/que, README and LICENSE.txt, tag v2.4.1:
  <https://github.com/que-rb/que>
- [solid-queue] rails/solid_queue, README and MIT-LICENSE, tag v1.7.0:
  <https://github.com/rails/solid_queue>
- [rails8] Heinemeier Hansson, D. "Rails 8.0: No PaaS Required," 2024-11-07:
  <https://rubyonrails.org/2024/11/7/rails-8-no-paas-required>
- [absurd] earendil-works/absurd, README and `sql/absurd.sql` at commit
  `5b97022ac6d1`, tag 0.5.0: <https://github.com/earendil-works/absurd>
- [mvcc-queue] Leach, B. "Postgres Job Queues & Failure By MVCC,"
  2015-05-18: <https://brandur.org/postgres-queues>
- [idem-keys] Leach, B. "Implementing Stripe-like Idempotency Keys in
  Postgres," 2017-10-27: <https://brandur.org/idempotency-keys>
- [k8s-cron] Kubernetes docs, "CronJob", section "CronJob limitations":
  <https://kubernetes.io/docs/concepts/workloads/controllers/cron-jobs/>
- [aws-retries] Amazon Builders' Library, "Timeouts, retries, and backoff
  with jitter," 2019-12-05 (the byline gives only "Marc" and the handle
  "@mbrooker". The full name is unverified):
  <https://builder.aws.com/content/3EumjoZascWd1oZiEgL8ORlv3qE/timeouts-retries-and-backoff-with-jitter>
- [sqs-dlq] Amazon SQS, "Using dead-letter queues":
  <https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-dead-letter-queues.html>
- In this repository: ADR 3, ADR 4, ADR 30, ADR 38, ADR 42, ADR 43,
  ADR 48, ADR 60, RFC 6, RFC 7, RFC 36 (draft pull request 671), issues 667
  to 670, `pkg/store/postgres/queries.sql` (`AcquireAuditSinkLease`),
  `scripts/trivy-license-gate.py`, `docs/research/licenses.md`.
