# 298. PostgreSQL failover readiness

Status: accepted · 2026-10-31. Work item W9 of milestone 7 (ADR 280). The design details are decisions of the PM under the owner's delegation, and the owner accepts them with the merge. Extends ADR 280 decision 4 and ADR 72. Does not change ADR 4 or the minimum topology of ADR 280 decision 5

Changes:

- Extends ADR 280 decision 4: what each instance does while the
  PostgreSQL primary fails over.
- Extends ADR 72: what each instance does while the PostgreSQL primary
  fails over.

Changed by:

- ADR 337 changes decision 9: the record of the last released lock is shown in the workspace API, and not in the inventory.

## Context

ADR 280 makes one installation run active-active on one PostgreSQL
database. A database service with high availability, for example Amazon RDS
Multi-AZ, Patroni or CloudNativePG, moves the primary role to a standby when
the primary fails. W9 asks what each instance does during that move, and
after it.

Some of the work is already on `main`:

- The store adapter translates connection errors, SQLSTATE class 08 codes,
  57P01, 57P02, 57P03, 53300 and 25006 to `namespace.ErrUnavailable`
  (`pkg/store/postgres/unavailable.go`). Each API answers 503 with
  `Retry-After: 5` (W3).
- The readiness probe answers 503 when the store ping fails.
- The pool reconnects by itself. The instance does not restart.
- The database URL can set `target_session_attrs`, `pool_max_conn_lifetime`
  and `pool_health_check_period`. The server passes them to pgx as they are.
  The several-instances guide documents them, with no default from the server.
- A durable operation renews its lease every 10 seconds. The runner counts
  the deadline from the start of the last renewal that succeeded, on the
  monotonic clock, minus a margin of `max(RenewEvery, Lease/4)`: 10 seconds.
  Each renewal has the time left as its timeout. So a lost or slow renewal
  answer makes the runner stop sooner, never later. The claim times of a
  durable operation come from the database clock (`clock_timestamp()`). The
  lease is 30 seconds (ADR 280 decision 8).
- A claim that fails with an error waits for the claim backoff of the
  durable runner: capped exponential with jitter, at most 30 seconds
  (`DefaultClaimBackoffMax`).
- A repeated tool write gets the answer of a success in these cases:
  - A Pulumi checkpoint with the same sequence and digest
    (`writeCheckpoint`, `checkSequence`).
  - A Pulumi journal batch with the same sequence IDs and SHA-256
    (`journalRetry`).
  - A Pulumi event: `AppendEvent` uses `ON CONFLICT DO NOTHING` on
    `(stack_id, update_id, sequence)`.
  - A Pulumi complete with the same status, and a lease renewal, which gives
    back the same token.
  - A TFE state version upload and finalize (`confirmRetry`).
- HA exit-test step 7 stops the database for 60 seconds, and for less than
  the retry window of the CLIs. The e2e proxy (`e2e/dbproxy.go`) cuts the
  connections of one instance. The guide gives the measured retry windows:
  13 to 14 seconds for the Pulumi CLI, more than 30 seconds for OpenTofu.

### What the services do

The facts below come from the vendor pages on 2026-10-31.

| Service | Failover time | Replication | Address after the failover | Old primary |
| --- | --- | --- | --- | --- |
| Amazon RDS Multi-AZ DB instance | "typically 60–120 seconds" | "a synchronous standby replica" | The DNS record of the DB instance changes to the standby. "You need to re-establish any existing connections." | Not stated |
| Amazon RDS Multi-AZ DB cluster | "typically under 35 seconds" | "semisynchronous replication, which requires acknowledgment from at least one reader DB instance" | The writer endpoint | Not stated |
| Patroni | The leader key expires 20 to 30 seconds after the failure (`ttl` 30, `loop_wait` 10, `retry_timeout` 10). A replica acts at its next loop, so about 20 to 40 seconds | Asynchronous unless `synchronous_mode: true`. An asynchronous replica can lag by up to `maximum_lag_on_failover` (1 MiB by default) and still be promoted | A proxy or a load balancer that uses `GET /primary` as its health check | Patroni demotes a leader that cannot update its key for longer than `retry_timeout`. That server stays up as a read-only server |
| CloudNativePG | `failoverDelay` is 0 seconds by default. A replica promotes only when it holds the primary lease (`.spec.primaryLease`, 15 seconds by default) | Asynchronous unless the cluster sets `.spec.postgresql.synchronous` | The `<cluster>-rw` service points to the primary | Shut down ("fast", then "immediate"). It restarts as a replica |

Sources:

- AWS DB instances:
  <https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/Concepts.MultiAZ.Failover.html>
  and
  <https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/Concepts.MultiAZSingleStandby.html>.
  AWS also recommends a DNS cache TTL of no more than 60 seconds for a JVM.
- AWS DB clusters:
  <https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/multi-az-db-clusters-concepts.html>
  and
  <https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/multi-az-db-clusters-concepts-failover.html>.
- Patroni: <https://patroni.readthedocs.io/en/latest/dynamic_configuration.html>.
  "DCS or network issues shorter than this [`retry_timeout`] will not cause
  Patroni to demote the leader."
- CloudNativePG: `docs/src/failover.md` and `docs/src/service_management.md`
  on the `main` branch of <https://github.com/cloudnative-pg/cloudnative-pg>.
  The `.spec.primaryLease` table with `leaseDurationSeconds` 15 is in
  `failover.md`, in the section "Safe primary election". The pages of the
  documentation site returned no text when we read them.
- libpq: <https://www.postgresql.org/docs/current/libpq-connect.html>.
  `target_session_attrs=read-write` needs a server that is not in hot standby
  and has `default_transaction_read_only` off. `primary` needs only a server
  that is not in hot standby.
- PostgreSQL error codes:
  <https://www.postgresql.org/docs/current/errcodes-appendix.html>. 25006 is
  `read_only_sql_transaction`. 08007 is `transaction_resolution_unknown`.
  57014 is `query_canceled`.
- pgx 5.11.0, read in the module source:
  - `pgconn.ParseConfig` accepts `target_session_attrs` with the values
    `any` (the default), `read-write`, `read-only`, `primary`, `standby` and
    `prefer-standby`. It checks the value only when it opens a connection.
    The `read-write` check reads `transaction_read_only`. A hot standby
    reports `on` there, so the check refuses a hot standby too.
  - `connect_timeout` has no default. With no value, only the context limits
    a dial.
  - The default dialer is a `net.Dialer` with no settings, so Go's default
    TCP keepalive applies.
  - pgx closes a connection after an error with the severity `FATAL`, for
    example 57P01. 25006 has the severity `ERROR`, so the connection stays in
    the pool.
  - On the end of a context, the default handler sets the deadline of the
    network connection to now. The statement fails, and pgx closes the
    connection. The handler sends no cancel request.
  - pgx then closes the broken connection in the background. That close
    sends a cancel request on a new connection, which `connect_timeout`
    bounds. Then it reads the old socket until the server closes it, for up
    to 15 seconds. The pool keeps the slot of the connection until the close
    ends.
  - The pool pings a connection before it gives it out when the connection
    was idle for more than 1 second.
  - `Pool.Reset` closes the idle connections at once, and each connection in
    use when it comes back.
  - `pool_max_conn_lifetime` is 1 hour by default, `pool_health_check_period`
    1 minute, `pool_max_conn_lifetime_jitter` 0.

### The gaps

1. **A stale connection to a read-only server.** After a failover, the old
   primary can stay up as a read-only server (Patroni demotes it), and a DNS
   cache can still give its address. A pool connection that opened before
   the failover stays open. Each write on it fails with 25006 and gets 503,
   until the connection reaches its maximum lifetime: 1 hour by default.
2. **Readiness does not see a read-only server.** The store check is a ping.
   A ping works on a read-only server, so an instance stays ready while each
   write fails.
3. **A database that does not answer and does not close the connection.**
   When the address of the old primary stops answering, a statement in
   flight waits for the TCP retransmission timeout of the host. On Linux,
   that is about 15 minutes by default. A dial with no `connect_timeout`
   waits for the SYN retries of the host. No store call has a deadline of
   its own. The HTTP server has no request timeout.
4. **No safe default for the pool.** With no settings in the URL,
   `target_session_attrs` is `any`, and a new connection can reach a
   read-only server.
5. **A commit with an unknown outcome.** When the connection breaks after
   the server sent `COMMIT` and before the answer came back, the store does
   not know if the transaction committed. The API answers 503. Some writes
   on `main` give an error to a repeated request when the first try
   committed (decision 9).
6. **A commit that a failover undoes.** With asynchronous replication, the
   new primary can miss commits that the old primary acknowledged. A client
   then got a success for a write that the database no longer holds.
7. **A database gap counts as a failure of a durable operation.** Each
   claim that expires in a gap adds 1 to `expiries`, and nothing resets the
   count. `DefaultExpiryLimit` is 3. A step error that wraps
   `ErrUnavailable` counts toward `MaxAttempts` (5), unless the `Deferred`
   function of the runner matches it. The backup, restore and audit chain
   runners have no `Deferred` function. A long backup or restore that meets
   three failovers fails with `ErrExpiryLimit`.
8. **No test of a real promotion.** Step 7 stops the database and starts it
   again. No test promotes a standby, keeps the old primary up as a
   read-only server, or drops a connection without a reset.

## Decision

1. **The instance stays up during a failover.** No instance stops or
   restarts because the database does not answer. The pool opens new
   connections when the database answers again. This is the behavior of
   today, and a test keeps it.
2. **Requests fail fast with 503.** During the gap, each request that needs
   the database gets 503 with `Retry-After: 5`, in the shape of its API (W3,
   ADR 280 decision 4). The server does not retry a request on its own. A
   second retry layer would hide the failure, and it would multiply the load
   on a database that starts again. The consequence: a failover that is
   longer than the retry window of the CLI makes the command fail
   (Consequences).
3. **Each database round trip has a deadline, on the client and on the
   server.**
   - **The client deadline.** The guarded wrappers of the adapter give each
     round trip a deadline of 30 seconds, or the deadline of the caller when
     that is earlier. The wrappers cover this full method set: `Exec`,
     `Query` and the read of its rows, `QueryRow` and its `Scan`,
     `SendBatch` and the reads of its results, `Begin`, `BeginTx`, `Commit`,
     `Rollback` and `CopyFrom`. `Conn` returns the raw `*pgx.Conn`, and no
     wrapper can wrap that type. So each call of `Conn` counts as a path that
     skips the wrappers: it is in the list of the long-path test below, and
     it names its bound. The
     deadline of a row read ends when the rows close or the `Scan` returns,
     not when `Query` returns. The wrappers set the deadline with
     `context.WithTimeoutCause` and a sentinel cause. `classify` translates
     a `DeadlineExceeded` to `storeerr.ConnectionFailed` only when
     `context.Cause` is that sentinel. The deadline of a caller stays the
     error of the caller, as today.
   - **The server timeouts.** Each connection of the pool sets these runtime
     parameters: `statement_timeout` 25 seconds,
     `idle_in_transaction_session_timeout` 60 seconds and
     `client_connection_check_interval` 5 seconds. So a slow statement on a
     server that answers ends on the server first, with 57014, and its locks
     go free. The connection stays in the pool. A backend whose client went
     away stops within 5 seconds and frees its locks. The client deadline
     stays for the case of a dead network.
   - The adapter translates 57014 to a new class,
     `storeerr.StatementTimeout`. The API answers 503 with `Retry-After`,
     and the log names the cause `statement-timeout`. A canceled context of
     the caller stays a canceled context, not an outage.
   - A path that runs for longer, for example the migrations, the backup
     export and import, the restore swap and the staging copy, names its own
     bound in code. The pool sets the server timeouts as start-up
     parameters of each connection, so `RESET ALL` goes back to them. A
     session `SET` on a connection that goes back to the pool stays on that
     connection for the next request, so it is not allowed. Each kind of
     path has one allowed form:
     - Statements inside a transaction: `SET LOCAL statement_timeout` and
       `SET LOCAL idle_in_transaction_session_timeout` in that transaction.
     - A single statement outside a transaction, for example `CREATE SCHEMA`
       or `DROP SCHEMA ... CASCADE`: the path runs it in a short transaction
       with the same two `SET LOCAL` statements.
     - A connection that the path keeps for a session (the sweep lock): the
       path changes no session value, so its statements run under the
       start-up values of the pool. When a path must change a session
       value, it ends the connection with `Hijack` and a close.
     - A second layer: each long path gets its connection from one helper
       of the store, and the helper marks that connection. The
       `AfterRelease` function of the pool runs `RESET ALL` only on a marked
       connection. So no session value of a long path reaches the next
       request. The statements of the API paths do not use the helper, so
       their releases get no extra round trip, and the benchmark of
       database round trips for each flow does not change.
     - `AfterRelease` gets no context, so the `RESET ALL` runs under its own
       deadline of 2 seconds. On any error, or at the deadline,
       `AfterRelease` returns `false`, and the pool destroys the connection.
       When the reset fails, `AfterRelease` also closes the network
       connection, so the background close of pgx does not wait 15 seconds
       on the old socket. A connection that stops answering then holds its
       pool slot for about 2 seconds plus the dial of the cancel request,
       which `connect_timeout` bounds. It does not hold the slot for the TCP
       timeout of the host.
     - After the statement deadline of an API path, the request ends at 30
       seconds, but the background close of pgx can keep the slot of its
       connection for up to 15 seconds longer. The guide states this. The
       server timeouts stay as a defense: they end a slow statement on a
       server that answers before the client deadline, and pgx then keeps
       the connection.

     - The migrations have their own named bound: 15 minutes for each round
       trip on the client, and `SET LOCAL` of 15 minutes for both server
       timeouts. Their first round trip waits for the migration lock while
       another instance migrates, and a migration of a large table can run
       for minutes.

     On `main` these paths
     are `pool.Acquire` in `inBackupTx`, `TryLockSweep`, and the direct
     `s.pool.Query` and `s.pool.Exec` calls of the backup code.
   - `TestLongPathsNameTheirBound` finds these paths with a static check: it
     parses the files of `pkg/store/postgres` with `go/ast` and finds each
     call of `Acquire`, `Hijack` and `Conn`, and each direct call on
     `s.pool`. A list kept by hand would drift. Each path that the check
     finds must be in the allowlist of the test. The test checks that the
     path uses one of the allowed forms above, not only that the allowlist
     names it. It fails on a session `SET` (a `SET` with no `LOCAL`) in a
     path that releases its connection to the pool, and on
     `set_config(..., false)`, which is a session `SET` with no `SET`
     keyword. The store uses only `set_config(..., true)` today. It also
     fails on a long path that does not get its connection from the
     helper. A new path fails the
     test until it names its bounds.
   - These bounds are code rules. They go into the catalog of code rules,
     and the installation API shows their values (decision 6).
4. **The pool leaves a read-only server.** When the adapter translates an
   error to `storeerr.ReadOnly`, it runs the write check of decision 7 on a
   new connection. When that check also finds a read-only server, the
   adapter calls `pool.Reset()`. The idle connections close at once. The
   connections in use close when they come back. New connections then pass
   the check of decision 5.
   - A 25006 from a transaction that the store opened `READ ONLY` (the
     inspect read, the snapshot read) is a bug, not a read-only server. The
     write check then passes, and the pool does not reset.
   - Decisions 4 and 7 share one limiter: at most one reset in each 5
     seconds for each pool.
   - Each reset writes a warning log with the reason `read-only`, and
     increments the counter `dustpan_store_pool_resets_total{reason}`. The
     operator can see each reset and why it happened.
5. **Safe pool defaults, with an allowlist.** At the start, the server reads
   the failover settings of the URL and checks each against an allowlist.
   When the URL does not name a setting, the server sets the default below.

   | Setting | Default | Allowed |
   | --- | --- | --- |
   | `target_session_attrs` | `read-write` | `read-write`, or `primary` (the server checks it as `read-write`) |
   | `connect_timeout` | `5` (seconds) | 1 to 60 |
   | `pool_max_conn_lifetime` | `30m` | 1m to 24h |
   | `pool_max_conn_lifetime_jitter` | `1m` | 0 to the lifetime |
   | `pool_health_check_period` | `10s` | 1s to 10m |

   - A value outside the allowlist stops the start with an error that names
     the setting, the value and the allowed range. The server does not change
     a value to make it valid. The error has a catalog code
     (`store.failover-setting-invalid`).
   - The server refuses `any`, `read-only`, `standby` and `prefer-standby`.
     The server writes, so a connection to a server that refuses writes is
     never correct.
   - The server accepts `primary` and treats it as the same value as
     `read-write`. It does not rewrite the URL. For both values, it installs
     its own pgx `ValidateConnect` function. The function reads
     `pg_is_in_recovery()` and `transaction_read_only` in one statement, and
     refuses the connection when either value says that the server refuses
     writes. libpq and
     pgx define `primary` with a weaker check: a server that is not in hot
     standby, also with `default_transaction_read_only` on. Each write on
     such a server fails, so the server applies the stronger check. The
     installation API shows the
     value of the URL (`primary`), its source (`url`) and the check that
     applies (`read-write`).
   - The server sets the TCP keepalive of the dialer with Go's
     `net.KeepAliveConfig`: idle 15 seconds, interval 5 seconds, 3 probes. A
     dead idle connection then closes in about 30 seconds. Go supports this
     on Linux and macOS, so no build tag is necessary.
   - The PM accepted these values and the statement deadline on 2026-10-31.
     An RDS failover takes 60 to 120 seconds, so the server fails fast and
     the clients retry. Nobody measured the values against a managed
     service. The fault test of decision 12 measures the recovery time with
     them, and the guide gives the measured figure and its date (ADR 280
     decision 6).
6. **The effective settings are visible.** The installation API returns the
   effective pool settings: each setting of decision 5, where its value came
   from (`url` or `default`), the check that applies, the client deadline
   and the server timeouts of decision 3. It also returns the replication
   facts of decision 10. The response never holds the URL, the host, the
   user or the password. The start log writes the same fields once.
7. **Readiness checks that the database accepts writes.** The store check of
   the readiness probe changes from a ping to one statement:
   `SELECT pg_is_in_recovery(), current_setting('transaction_read_only')`.
   When either value says that the server refuses writes, the check fails
   with the name `store-read-only`, and the probe answers 503. The adapter
   also resets the pool, under the limiter of decision 4. A store with no
   such check (SQLite) keeps the ping. The body names the check and holds no
   driver text.
8. **No half writes, and no resend.**
   - A database transaction commits all its rows or none. A connection that
     breaks before `COMMIT` rolls the transaction back on the server.
   - The server does not send a transaction again after an error. It does
     not know if a `COMMIT` with an unknown outcome committed.
   - A blob that a request wrote before a rolled-back commit stays an
     orphan. The existing purge deletes it after its grace period. Its
     delete also waits for a running backup (ADR 280 decision 7).
9. **A write after an unknown commit.** Each write of the tool-compatible
   APIs must give a correct result in two cases, when the first try
   committed and the client did not get its answer:
   - (a) The CLI sends the same request again.
   - (b) The user runs the next command.

   "Correct" means the answer of a success, or an answer that the CLI
   handles as the end of the write. A 409 or a 423 that stops the command is
   not correct, unless this table lists it as an accepted exception. What
   makes a repeated request safe is a sequence number, a serial with a
   checksum, or a lock ID. Neither CLI sends the `Idempotency-Key` header,
   so it does not count for these writes.

   **How the Pulumi CLI retries.** The client of the Pulumi CLI, read in its
   source at the tags v3.200.0 and v3.272.0
   (`pkg/backend/httpstate/client/api.go` and `client.go`), retries on a 5xx
   answer only the calls that use the policy `retryAllMethods`: the lease
   renewal, the three checkpoint calls, the journal entries, the engine
   events, cancel, complete, and the encrypt and decrypt calls. The other
   calls use the default policy `retryGetMethod`, so the CLI does not send a
   POST again after a 503. `CreateStack`, `CreateUpdate` and `StartUpdate`
   are such POST calls. A retry waits 1 second, then doubles the wait, for at
   most 4 retries: 1 + 2 + 4 + 8 = 15 seconds. That agrees with the retry
   window of 13 to 14 seconds that the e2e suite measured. The e2e records
   of the suite are files of each run and are not in the repository, and
   step 7 meets only the calls that run during its outage. So step 9 adds a
   check of the retry of each route: the round-robin HTTP proxy of
   `HACluster` (`e2e/ha.go`) answers 503 to one route at a time, and the record shows if the CLI sent the request again.

   | Write | CLI sends it again on 503 | Today on `main` | Decision |
   | --- | --- | --- | --- |
   | Pulumi checkpoint, journal batch, event, complete, renew | Yes | Success on a repeat (Context) | No change |
   | Pulumi `CreateStack` | No | The stack exists. The next `pulumi stack init` gets 409, and `pulumi up` works | No change. The guide names the case |
   | Pulumi `CreateUpdate` | No | An update that did not start stays. The next command makes a new update | No change |
   | Pulumi `StartUpdate` | No | `StartBy` commits a running update. The server does not store the token. The command fails at once. The next `pulumi up` gets 409 for up to `DefaultLease`, 5 minutes after the start, or until `pulumi cancel` | An accepted exception (PM). The guide gives the remedy `pulumi cancel` and the block of at most 5 minutes |
   | TFE state version upload and finalize | Yes (OpenTofu) | Success on a repeat (`confirmRetry`) | No change |
   | HTTP backend `LOCK` with the same lock ID | Yes (OpenTofu) | `lockAs` refuses any held lock with `ErrLocked`: 423, and `tofu` fails with its own lock in place | Success when the lock is still held, with the same lock ID and the same holder. The client chose the ID, so a match is the same request. The handler pre-check and the service both apply the rule, before the refusal of a held lock |
   | HTTP backend `UNLOCK`, TFE `actions/unlock`, after a committed unlock | Yes (OpenTofu) | HTTP backend: the handler pre-check (`!ws.Lock.Locked() \|\| ws.Lock.ID != info.ID`) answers 409 with no body. Below it, `UnlockLock` runs `checkLockID` first and gives `ErrLockMismatch`. TFE: `heldBy` gives `ErrNotLocked`, 409 `workspace.not-locked` | The workspace row keeps a record of its last released lock. The record holds the lock ID, the holder, the releaser, the time of the release, and the kind of release: normal or forced. In a forced release, the releaser differs from the holder. The API of the workspace shows it. On a free workspace, the HTTP backend `UNLOCK` answers success only when the record has the lock ID of the request. The release must also be a normal release, not a forced one. TFE `actions/unlock` sends no lock ID. So it answers success only when the record names the caller as the holder, after a normal release. After a forced release, the original holder gets 409, so the operator and the CLI see that someone broke the lock. A force-unlock is a write that OpenTofu also sends again after a 503. On a free workspace, a repeated force-unlock answers success when the record shows a forced release by the same caller. For the HTTP backend, that is an `UNLOCK` from a caller that is not the holder, with the lock ID of the record. For TFE, that is `actions/force-unlock` from the caller that the record names as the releaser. Today `forceUnlock` gives `ErrNotLocked` on a free workspace, and the TFE answer is 409. In all other cases, 409 as today. The record matches for 24 hours after the release, and not after that. No tool lock has a lease to use as the bound: an OpenTofu lock has none. 24 hours is far above each retry window of the CLIs: 15 seconds for the Pulumi CLI, more than 30 seconds plus `-lock-timeout` for OpenTofu. And a record cannot match an unlock from a later day. The rule goes in the handler pre-check and in the service, ahead of `checkLockID`. No state changes, so no audit entry. `make e2e` checks each CLI version |
   | TFE `actions/lock` sent again | Yes (OpenTofu) | 409 "is locked by" with the same subject. OpenTofu waits under `-lock-timeout`, then fails | An accepted exception (PM): the answer stays 409, and the lock fails closed. The real TFE also refuses a lock on a locked workspace. A match on the principal or the token cannot tell a retry from a second run. The lock record names the holder and the time. The guide gives the remedy: `tofu force-unlock` |

   - The record is a change of the workspace store port. Its conformance
     rows come first, before either adapter implements it. SQLite and
     PostgreSQL both get the migration. The backup table list holds the new
     columns, so `backup_coverage_test` passes.
   - Step 9d (decision 12) adds a row for each lock, unlock and force-unlock
     route and for `StartUpdate`. The rows include a repeated force-unlock
     by the same caller (success) and a normal unlock by the original holder
     after a forced release (409).
   - The guide changes its sentence "OpenTofu: ... the workspace has no
     lock": after a lost answer to a TFE lock, the lock can stay.
10. **Replication mode (PM).** The guarantees of decisions 8, 9 and 11 hold
    only when a failover cannot undo a commit that the client saw succeed.
    That needs synchronous replication to at least one standby:
    - Amazon RDS Multi-AZ DB instances replicate synchronously. Multi-AZ DB
      clusters replicate semisynchronously, with an acknowledgment from at
      least one reader.
    - Patroni: `synchronous_mode: true`. Add `synchronous_mode_strict: true`
      when writes must stop, not continue without a standby.
    - CloudNativePG: `.spec.postgresql.synchronous` with `number: 1` and
      `dataDurability: required`.

    Asynchronous replication stays allowed. With it, a failover can lose a
    commit that the server acknowledged. These are the losses:
    - A state serial or a state version that OpenTofu saw saved.
    - A Pulumi checkpoint, journal batch or event.
    - An unlock: the workspace is locked again after the failover.
    - A fence promotion of a namespace.
    - The renewal (`Extend`) of a lease. The claim then expires earlier than
      the runner counts, and two runners can hold one claim.

    The guide gives this list. There is no silent fallback:
    - The installation API (decision 6) shows the values of
      `synchronous_commit` and `synchronous_standby_names` as the database
      reports them, under the name "PostgreSQL-level synchronous
      replication". The API text says that storage-level replication is not
      visible to the server. An RDS Multi-AZ DB instance and Aurora
      replicate at the storage level, and there `synchronous_standby_names`
      is empty.
    - The setting `database-replication` lets the operator declare the mode:
      `postgres` (the default: the server reads the facts above),
      `storage-sync` (the service replicates synchronously at the storage
      level, for example RDS Multi-AZ DB instances and Aurora) or `async`
      (the operator accepts the losses above). The API shows the declared
      value with the source `operator` or `default`, next to the reported
      facts. Another value stops the start with
      `store.failover-setting-invalid`.
    - The start log writes a warning that names the losses when neither
      source says synchronous: the facts of the database show no
      PostgreSQL-level synchronous standby, and the setting is not
      `storage-sync`. So a declared `async` also gets the warning. A correct
      `storage-sync` setup gets no false alarm. The server does not try to
      detect the host or the service.
    - The guide gives the setting for RDS and Aurora.
    - The readiness probe does not fail on the mode. This decision
    does not change the minimum topology of ADR 280 decision 5.
11. **Leases and durable operations across the gap.**
    - The lease stays at 30 seconds (ADR 280 decision 8).
    - A gap shorter than the lease: the renewals fail, the step continues,
      and the next renewal after the gap extends the claim.
    - A gap longer than the lease: the runner stops the step at the lease
      deadline, as it does today. When the database answers again, any
      instance claims the operation with a new fencing token, and the step
      runs again (ADR 280 decision 3).
    - **A database gap does not count against the limits.** The `Deferred`
      function of every runner matches only the classes that mean that the
      database cannot be reached: `ConnectionFailed`, `Shutdown` and
      `ReadOnly`. A new domain sentinel, `namespace.ErrUnreachable`, wraps
      these three classes. Each `storeerr.Error` still wraps
      `namespace.ErrUnavailable` too, so the HTTP answer stays 503. A step
      error from a gap gives the attempt back with no count.
      `StatementTimeout` and `Capacity` count as normal failures: a step
      whose statement always takes longer than the server timeout then
      reaches `MaxAttempts` and fails, and it does not loop for ever. An
      expiry
      does not count when the instance that held the claim wrote a
      heartbeat after the claim expired: a process that lived through a gap
      did not crash. The store checks this at the takeover, and again
      before `ErrExpiryLimit` ends an operation. The implementation tests
      both cases: a gap does not count, a crash does.
    - The rule needs one stable owner for each process. Today
      `durableOwner()` makes a new ID on each call when the instance ID is
      empty, which fails safe (each gap counts), and `auditChainOwner()`
      returns the constant `audit-chain`, which fails open (a crash never
      counts). `NewServer` sets one random instance ID at each start of the
      process when the embedder gives none. `durableOwner()`,
      `auditChainOwner()`, every runner and the heartbeat use that ID.
      `auditChainOwner()` no longer returns a shared constant, so the rule
      fails safe.
    - **The sweep lock.** `TryLockSweep` holds a session advisory lock on one
      connection for the whole retention sweep. Advisory locks are not
      replicated. After a failover, the lock connection of instance A is
      dead, but its sweep can continue, and instance B can take the lock on
      the new primary and start a second sweep. So the sweep checks its lock
      connection with `SELECT 1` before each batch, and stops on an error.
      The window shrinks to one batch. Each delete of a batch must be safe
      to run twice. A test runs two sweeps at the same time and checks the
      result. `TryLockSweep` is in the list of decision 3, with its bounds.
    - The claim times of durable operations and of audit sink leases come
      from the database clock. After a failover, the clock of the new
      primary counts. A skew between the old and the new primary must stay
      below the runner margin of 10 seconds (`max(RenewEvery, Lease/4)`).
      Above that, two holders are possible. The guide gives this bound, not
      only "use NTP".
    - Notification delivery leases use the clock of the instance
      (`req.Now`). A skew between instances can give two holders and a
      duplicate send. This ADR does not change it. The guide names it.
    - The claim loop, the schedules, the heartbeat and the audit sink
      shipping back off with jitter on `namespace.ErrUnavailable`. A test of
      each loop checks that the number of store calls during a gap of 60
      seconds stays below a bound.
    - The update lease of a Pulumi update and the lock of an OpenTofu
      workspace do not change.
12. **A failover fault test in `make e2e-ha`.** HA exit-test step 9,
    "database failover", runs with three instances behind the round-robin
    proxy:
    - The test starts a PostgreSQL primary and a synchronous streaming
      standby (`initdb`, `pg_basebackup -R`, `pg_ctl`,
      `synchronous_standby_names`) from the PostgreSQL binaries of the host.
      The environment variable `DUSTPAN_TEST_PG_BIN` names their directory.
      Without it, the step calls `t.Skip` with a message that names the
      variable, and the suite log shows it. The other steps run. The nightly
      e2e-ha workflow sets the variable, so the step cannot skip there
      without notice. The pull request of the implementation gives the
      install step of the nightly workflow as a file that the owner can
      copy, as the S3 benchmark does, because a change to a workflow file
      needs the token of the owner.
    - The instances reach the database through the e2e database proxy. The
      proxy stands for the DNS name of the service.
    - **9a, promotion.** While `pulumi up` and `tofu apply` run, and a
      durable operation runs, the test stops the primary with
      `pg_ctl stop -m immediate`, promotes the standby, and points the proxy
      at it. Each command completes, or fails with a 503 that the guide
      describes. The state after the failover is consistent, and the next
      preview and plan show no changes after the remedy that the guide
      gives. The durable operation ends succeeded, with no count against
      its limits. The test measures the time from the stop to the first
      write that succeeds on each instance.
    - **9b, stale read-only primary.** The old primary starts again as a
      read-only server, and the proxy sends the new connections of one
      instance to it. That instance answers 503 and turns unready with
      `store-read-only`. When the proxy points at the new primary again, the
      instance turns ready within one health check period, with no restart.
    - **9c, a connection that stops without a reset.** A new proxy mode
      holds the bytes of each connection and closes nothing. Each instance
      turns unready, and each request ends with 503 within the client
      deadline plus the readiness timeout. No request waits for the TCP
      timeout of the host.
    - **9d, a lost commit answer**, in the store adapter tests, not in e2e:
      a proxy that reads the frontend messages forwards a `COMMIT`, and
      closes the connection before the answer reaches the client. The test
      checks that the store returns `namespace.ErrUnavailable`. Then it
      checks the result of decision 9 for each row of its table, for the
      same request again and for the next command.
    - The proxy stays in the repository (`e2e/dbproxy.go`). Toxiproxy is not
      necessary: the modes are small, and the e2e suite then needs no other
      container.

### Ports and tests

The implementation adds these names. They are not Go code in this pull
request, because a port with no caller fails the `unused` lint. Step 9 of
decision 12 is the one test that can skip, and only when the PostgreSQL
binaries are not present.

- `pkg/store/postgres`: `FailoverSettings` (the effective settings of
  decision 6) and `(*Store).FailoverSettings() FailoverSettings`.
- `pkg/store/internal/storeerr`: the class `StatementTimeout`, and the
  wrap of `namespace.ErrUnreachable` for the classes `ConnectionFailed`,
  `Shutdown` and `ReadOnly`.
- `namespace.Store` gets no new method. A new optional interface in
  `pkg/dustpan`, `writeChecker` with `CheckWritable(ctx) (bool, error)`, is
  what the readiness probe reads, in place of the ping of the store. The
  PostgreSQL store implements it. It returns false when the database answers
  that it refuses writes, and the probe then names the check
  `store-read-only`. It returns an error that wraps `namespace.ErrUnavailable`
  when the check cannot run, and the probe then fails closed with the check
  `store`.
- `internal/errcode`: `store.failover-setting-invalid`.
- Tests in `pkg/store/postgres`:
  - `TestFailoverSettingsAllowlist`: one row for each setting and each edge
    of its range, the refused values of `target_session_attrs`, and
    `primary` with the `read-write` check.
  - `TestStatementDeadlineClosesStalledConnection`: a proxy that holds the
    bytes. One row for each method of decision 3, including `SendBatch`,
    `Rollback` and a row read. Each call ends with `ErrUnavailable` within
    the deadline. A row read on a server that answers still works. One more
    row: the release of a marked connection while the proxy holds the
    bytes. `AfterRelease` returns `false` within 2 seconds, and the pool
    has a free slot again.
  - `TestServerStatementTimeout`: a statement longer than
    `statement_timeout` ends with `StatementTimeout`, and the connection
    stays in the pool.
  - `TestReadOnlyErrorResetsPool`: after one 25006 from a read-only server,
    the next write uses a new connection. A burst of 25006 errors gives one
    reset. A 25006 from a `READ ONLY` transaction gives no reset.
  - `TestCheckWritableOnReadOnlyServer`: a hot standby and a server with
    `default_transaction_read_only = on`.
  - `TestLostCommitAnswer`: decision 12, 9d.
  - `TestLongPathsNameTheirBound`: the list of decision 3.
  - `TestExpiryInGapDoesNotCount`: a gap does not count, a crash does.
- Tests in `pkg/workspace` and the HTTP adapters: the repeated lock and
  unlock and force-unlock of decision 9, with one row for a matching record
  and one row for each kind of mismatch: another lock ID, another caller,
  a forced release for the original holder, a repeated force-unlock by the
  same releaser, and a record older than 24 hours.
- `e2e/ha_failover_test.go`: the check of the retry of each Pulumi and
  OpenTofu route of decision 9, with the round-robin HTTP proxy of
  `HACluster` that answers 503 to one route at a time.
- Tests in `pkg/dustpan`: `TestReadinessStoreReadOnly`.
- Tests in `pkg/durable`: `TestClaimLoopBacksOffDuringOutage`,
  `TestUnavailableStepIsDeferred` (one row for each class: the three
  classes of `ErrUnreachable` are deferred, `StatementTimeout` and
  `Capacity` count), and one test for each loop of decision 11.
- `pkg/namespace`: the sentinel `ErrUnreachable`.
- Tests in `pkg/dustpan`: `TestOneOwnerForEachProcess`.
- Tests of the retention sweep: `TestSweepStopsWhenLockConnectionDies`
  and `TestTwoSweepsAtOnce`.
- Tests of the settings: `database-replication` with each allowed value and
  one value that is not allowed.
- `e2e/ha_failover_test.go`: steps 9a to 9c.

## Consequences

- **The Pulumi CLI fails during most managed failovers.** The guide measured
  a retry window of 13 to 14 seconds for the Pulumi CLI. A failover takes 60
  to 120 seconds on an RDS Multi-AZ DB instance, under 35 seconds on a DB
  cluster, and about 20 to 40 seconds on Patroni with its defaults. The
  server does not retry (decision 2). So a `pulumi up` that runs during such
  a failover fails, and its update stays running until its lease ends or
  someone runs `pulumi cancel`. The guide states this, with the figure that
  step 9 measures. OpenTofu retried for more than 30 seconds in the same
  measurement.
- The server sets pool settings that pgx does not set by default. A URL can
  still set each one inside its range. A URL with a value outside the range
  stops the start, also a URL that worked before. No tag shipped, so no
  upgrade path is necessary.
- A URL with `target_session_attrs=any`, `read-only`, `standby` or
  `prefer-standby` stops the start.
- The readiness probe sends one more statement than a ping.
- A statement that runs for more than 25 seconds on an API path ends on the
  server with 503. A slow query then shows up as a 503 that names the cause
  in the log, and not as a request that hangs.
- The guarantees of decisions 8 to 11 need synchronous replication. With
  asynchronous replication, a failover can lose acknowledged writes.
- A repeated HTTP backend `LOCK` changes from an error to a success. A
  repeated unlock changes to a success when the record of the last released
  lock matches. The workspace row gets the columns of that record in a new
  migration. A repeated TFE lock and a lost `StartUpdate` answer stay
  errors, with a documented remedy.
- Step 9 runs only on a runner with the PostgreSQL server binaries.
- The several-instances guide gets the effective defaults, the table of
  decision 9, the replication mode, the clock bounds, the readiness check,
  the DNS advice and the measured figures of step 9. The configuration
  reference lists the settings and their ranges.
- The roadmap gets exit-test step 9 for milestone 7.

## Answers of the PM

The PM answered the open points of the draft on 2026-10-31:

1. The values of decision 5 and the statement deadline of 30 seconds are
   accepted.
2. Step 9 runs where `DUSTPAN_TEST_PG_BIN` names the binaries, and reports
   a skip with its reason on other runners. The install step of the nightly
   workflow comes as a file that the owner copies.
3. The server accepts `target_session_attrs=primary` and checks it as
   `read-write` (decision 5).

The PM answered the architecture review on 2026-10-31:

4. The guarantees need a synchronous standby. Asynchronous replication
   stays allowed, with its losses in the guide and the effective values in
   the API (decision 10).
5. A repeated HTTP backend `LOCK` with the same lock ID and holder answers
   success. An unlock on a free workspace answers success only when the
   record of the last released lock matches. Otherwise it answers 409
   (decision 9).
6. A repeated TFE `actions/lock` keeps 409, and a lost `StartUpdate` answer
   keeps 409 for up to 5 minutes. Both are accepted exceptions with a
   remedy in the guide (decision 9).
7. The server timeouts of decision 3 and the answer to 57014 are decisions
   of this ADR.

The implementation (2026-11-01) corrected decision 3: the bound of a
stalled release includes the dial of the cancel request of pgx, the
background close of pgx can keep the slot of a connection after the
statement deadline, and the migrations have their own bound. The check of
the changes after round 3 (2026-10-31) changed decision 3: the
`RESET ALL` of `AfterRelease` runs only on a marked connection, under a
deadline of 2 seconds. The architecture review of round 3 changed
decisions 3 and 9:
the allowed forms of the server timeouts on the long paths, and a repeated
force-unlock. The architecture review of round 2 changed decisions 3, 9, 10
and 11: the static check of the long paths, the record check in the handler
and the service, the forced release, the declared replication mode, the
classes that `Deferred` matches, the stable owner of a process, and the
sweep lock.
