# 298. PostgreSQL failover readiness

Status: accepted · 2026-10-31. Work item W9 of milestone 7 (ADR 280). The design details are decisions of the PM under the owner's delegation, and the owner accepts them with the merge. Extends ADR 280 decision 4 and ADR 72. Does not change ADR 4

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
- A durable operation renews its lease every 10 seconds, and stops its step
  when no renewal succeeded before the lease ends. Lease times come from the
  database clock (`clock_timestamp()`). The lease is 30 seconds (ADR 280
  decision 8).
- HA exit-test step 7 stops the database for 60 seconds, and for less than
  the retry window of the CLIs. The e2e proxy (`e2e/dbproxy.go`) cuts the
  connections of one instance.

### What the services do

The facts below come from the vendor pages on 2026-10-31.

| Service | Failover time | Address after the failover | Old primary |
| --- | --- | --- | --- |
| Amazon RDS Multi-AZ | "typically 60–120 seconds" | The DNS record of the DB instance changes to the standby. "You need to re-establish any existing connections." | Not stated |
| Patroni | With the defaults (`ttl` 30, `loop_wait` 10, `retry_timeout` 10), the failover starts after the leader key expires: up to 30 seconds | A proxy or a load balancer that uses `GET /primary` as its health check | Not stated on the configuration page |
| CloudNativePG | `failoverDelay` is 0 seconds by default. The primary lease is 15 seconds by default | The `<cluster>-rw` service points to the primary | Shut down ("fast", then "immediate"). It restarts as a replica |

Sources:

- AWS: <https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/Concepts.MultiAZ.Failover.html>.
  AWS also recommends a DNS cache TTL of no more than 60 seconds for a JVM.
- Patroni: <https://patroni.readthedocs.io/en/latest/dynamic_configuration.html>.
- CloudNativePG: `docs/src/failover.md` and `docs/src/service_management.md`
  in <https://github.com/cloudnative-pg/cloudnative-pg>. The pages of the
  documentation site returned no text when we read them.
- libpq: <https://www.postgresql.org/docs/current/libpq-connect.html>.
  `target_session_attrs=read-write` needs a server that is not in hot standby
  and has `default_transaction_read_only` off. `primary` needs only a server
  that is not in hot standby.
- PostgreSQL error codes:
  <https://www.postgresql.org/docs/current/errcodes-appendix.html>. 25006 is
  `read_only_sql_transaction`. 08007 is `transaction_resolution_unknown`.
- pgx 5.11.0, read in the module source:
  - `pgconn.ParseConfig` accepts `target_session_attrs` with the values
    `any` (the default), `read-write`, `read-only`, `primary`, `standby` and
    `prefer-standby`. It checks the value only when it opens a connection.
  - `connect_timeout` has no default. With no value, only the context limits
    a dial.
  - The default dialer is a `net.Dialer` with no settings, so Go's default
    TCP keepalive applies.
  - pgx closes a connection after an error with the severity `FATAL`, for
    example 57P01. 25006 has the severity `ERROR`, so the connection stays in
    the pool.
  - On the end of a context, the default handler sets the deadline of the
    network connection to now. The statement fails, and pgx closes the
    connection.
  - The pool pings a connection before it gives it out when the connection
    was idle for more than 1 second.
  - `Pool.Reset` closes the idle connections at once, and each connection in
    use when it comes back.
  - `pool_max_conn_lifetime` is 1 hour by default, `pool_health_check_period`
    1 minute, `pool_max_conn_lifetime_jitter` 0.

### The gaps

1. **A stale connection to a read-only server.** After a failover, the old
   primary can stay up as a read-only server, and a DNS cache can still give
   its address. A pool connection that opened before the failover stays
   open. Each write on it fails with 25006 and gets 503, until the connection
   reaches its maximum lifetime: 1 hour by default.
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
   not know if the transaction committed. The API answers 503. The CLI sends
   the same request again. Nobody checked that each write of the
   tool-compatible APIs gives a correct answer when the first try did commit.
6. **No test of a real promotion.** Step 7 stops the database and starts it
   again. No test promotes a standby, keeps the old primary up as a
   read-only server, or drops a connection without a reset.

## Decision

1. **The instance stays up during a failover.** No instance stops or
   restarts because the database does not answer. The pool opens new
   connections when the database answers again. This is the behavior of
   today, and a test keeps it.
2. **Requests fail fast with 503.** During the gap, each request that needs
   the database gets 503 with `Retry-After: 5`, in the shape of its API (W3,
   ADR 280 decision 4). The server does not retry a request on its own. The
   CLIs already retry 503 (the several-instances guide gives the measured
   windows). A second retry layer would hide the failure, and it would
   multiply the load on a database that starts again.
3. **Each database round trip has a deadline.** The guarded wrappers of the
   adapter (`guardedDB`, `guardedTx`, `guardedPool`) give each statement,
   each `BEGIN` and each `COMMIT` a deadline of 30 seconds, or the deadline of
   the caller when that is earlier. The name of the bound is the statement
   deadline. At the deadline, pgx closes the connection, and the adapter
   translates the error to `storeerr.ConnectionFailed`, so the API answers
   503. A canceled request context stays a canceled context, not an outage.
   - A path that runs for longer, for example the migrations, the backup
     export and import, the restore swap and the staging copy, must not use
     the guarded wrappers with this deadline. Each such path names its own
     bound in code. The implementation finds each path that uses the pool
     with no wrapper, and a test lists them by name. A new path that skips
     the wrappers fails that test until it names its bound.
   - The statement deadline is a code rule. It goes into the catalog of code
     rules, and the installation API shows its value (decision 6).
4. **The pool leaves a read-only server at the first 25006.** When the
   adapter translates an error to `storeerr.ReadOnly`, it calls
   `pool.Reset()` once. The idle connections close at once. The connections
   in use close when they come back. New connections then pass the
   `target_session_attrs` check of decision 5.
   - At most one reset in each 5 seconds, so a burst of errors does not
     close the pool again and again.
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
     The server writes, so a connection to a server that refuses writes is never
     correct.
   - The server accepts `primary` and treats it as the same value as
     `read-write`: each new connection gets the `read-write` check. libpq
     and pgx define `primary` with a weaker check: a server that is not in
     hot standby, also with `default_transaction_read_only` on. Each write
     on such a server fails, so the server applies the stronger check. The
     installation API shows the value of the URL (`primary`) and the check
     that applies (`read-write`), so the operator can see the difference.
   - The server sets the TCP keepalive of the dialer with Go's `net.KeepAliveConfig`:
     idle 15 seconds, interval 5 seconds, 3 probes. A dead idle connection
     then closes in about 30 seconds. Go supports this on Linux and macOS, so
     no build tag is necessary.
   - The PM accepted these values and the statement deadline on 2026-10-31.
     An RDS failover takes 60 to 120 seconds, so the server fails fast and
     the clients retry. Nobody measured the values against a managed
     service. The fault test of decision 10 measures the recovery time with
     them, and the guide gives the measured figure and its date (ADR 280
     decision 6).
6. **The effective settings are visible.** The installation API returns the
   effective pool settings: each setting of decision 5, where its value came
   from (`url` or `default`), and the statement deadline. The response never
   holds the URL, the host, the user or the password. The start log writes
   the same fields once.
7. **Readiness checks that the database accepts writes.** The store check of
   the readiness probe changes from a ping to one statement:
   `SELECT pg_is_in_recovery(), current_setting('transaction_read_only')`.
   When either value says that the server refuses writes, the check fails
   with the name `store-read-only`, and the probe answers 503. The adapter
   also resets the pool, as in decision 4. A store with no such check
   (SQLite) keeps the ping. The body names the check and holds no driver
   text.
8. **No half writes. A retry after an unknown commit is correct.**
   - A database transaction commits all its rows or none. A connection that
     breaks before `COMMIT` rolls the transaction back on the server.
   - The server does not send a transaction again after an error. It does
     not know if a `COMMIT` with an unknown outcome committed.
   - Each write of the tool-compatible APIs must give a correct answer to a
     request that the CLI sends again after a 503, also when the first try
     committed. "Correct" means the answer of a success, or an answer that
     the CLI handles as the end of the write. A 409 that stops the command
     is not correct.
   - The implementation lists each write of the Pulumi Cloud-compatible API,
     the TFE-compatible API and the OpenTofu http backend in a table in the
     several-instances guide. The table names what makes the repeated
     request safe: a sequence number, a serial with a checksum, a lock ID, or
     a replay record of the `Idempotency-Key` header. A write that the audit
     finds unsafe gets a change in the same pull request, or an issue that the
     PM accepts.
   - A blob that a request wrote before a rolled-back commit stays an
     orphan. The existing purge deletes it after its grace period. Its
     delete also waits for a running backup (ADR 280 decision 7).
9. **Leases and durable operations across the gap.**
   - The lease stays at 30 seconds (ADR 280 decision 8).
   - A gap shorter than the lease: the renewals fail, the step continues,
     and the next renewal after the gap extends the claim.
   - A gap longer than the lease: the runner stops the step at the lease
     deadline, as it does today. When the database answers again, any
     instance claims the operation with a new fencing token, and the step
     runs again. Each step can run again (ADR 280 decision 3).
   - Lease times come from the database clock. After a failover, the clock
     of the new primary counts. A clock skew between the primary and the
     standby moves each expiry by that skew. The guide tells the operator to
     keep the database hosts on NTP.
   - The claim loop, the schedules, the heartbeat and the audit sink
     shipping back off with jitter on `namespace.ErrUnavailable`. A test of
     each loop checks that the number of store calls during a gap of 60
     seconds stays below a bound, so a gap does not cause a storm of
     connections when the database starts again.
   - The update lease of a Pulumi update and the lock of an OpenTofu
     workspace do not change. The guide already tells what an operator does
     after a gap that is longer than the retry window of the CLI.
10. **A failover fault test in `make e2e-ha`.** HA exit-test step 9,
    "database failover", runs with three instances behind the round-robin
    proxy:
    - The test starts a PostgreSQL primary and a streaming standby
      (`initdb`, `pg_basebackup -R`, `pg_ctl`) from the PostgreSQL binaries
      of the host. The environment variable `DUSTPAN_TEST_PG_BIN` names
      their directory. Without it, the step reports a skip with the reason,
      which names the variable, and the suite log shows it. The other steps
      run. The pull request of the implementation also gives the install
      step of the nightly workflow as a file that the owner can copy, as
      the S3 benchmark does, because a change to a workflow file needs the
      token of the owner.
    - The instances reach the database through the e2e database proxy. The
      proxy stands for the DNS name of the service.
    - **9a, promotion.** While `pulumi up` and `tofu apply` run, the test
      stops the primary with `pg_ctl stop -m immediate`, promotes the
      standby, and points the proxy at it. Both commands complete, or fail
      with a 503 that the guide describes. The state after the failover is
      consistent, and the next preview and plan show no changes. The test
      measures the time from the stop to the first write that succeeds on
      each instance.
    - **9b, stale read-only primary.** The old primary starts again as a
      read-only server, and the proxy sends the new connections of one
      instance to it. That instance answers 503 and turns unready with
      `store-read-only`. When the proxy points at the new primary again, the
      instance turns ready within one health check period, with no restart.
    - **9c, a connection that stops without a reset.** A new proxy mode
      holds the bytes of each connection and closes nothing. Each instance
      turns unready, and each request ends with 503 within the statement
      deadline plus the readiness timeout. No request waits for the TCP
      timeout of the host.
    - **9d, a lost commit answer**, in the store adapter tests, not in e2e:
      a proxy that reads the frontend messages forwards a `COMMIT`, and
      closes the connection before the answer reaches the client. The test
      checks that the store returns `namespace.ErrUnavailable`, and that the
      same write, sent again, gives the answer of decision 8.
    - The proxy stays in the repository (`e2e/dbproxy.go`). Toxiproxy is not
      necessary: the modes are small, and the e2e suite then needs no other
      container.

### Ports and tests

The implementation adds these names. They are not code in this pull request:
a port with no caller fails the lint, and a test that skips is not allowed.

- `pkg/store/postgres`: `FailoverSettings` (the effective settings of
  decision 6) and `(*Store).FailoverSettings() FailoverSettings`.
- `namespace.Store` gets no new method. A new optional interface in
  `pkg/dustpan`, `writeChecker` with `CheckWritable(ctx) error`, is what the
  readiness probe reads. The PostgreSQL store implements it. The error wraps
  `namespace.ErrUnavailable`.
- `internal/errcode`: `store.failover-setting-invalid`.
- Tests in `pkg/store/postgres`:
  - `TestFailoverSettingsAllowlist`: one row for each setting and each edge
    of its range, and the refused values of `target_session_attrs`.
  - `TestStatementDeadlineClosesStalledConnection`: a proxy that holds the
    bytes. The call ends with `ErrUnavailable` within the deadline.
  - `TestReadOnlyErrorResetsPool`: after one 25006, the next write uses a new
    connection. A burst of 25006 errors gives one reset.
  - `TestCheckWritableOnReadOnlyServer`: a hot standby and a server with
    `default_transaction_read_only = on`.
  - `TestLostCommitAnswer`: decision 10, 9d.
  - `TestLongPathsNameTheirBound`: the list of decision 3.
- Tests in `pkg/dustpan`: `TestReadinessStoreReadOnly`.
- Tests in `pkg/durable`: `TestClaimLoopBacksOffDuringOutage`, and one for
  each loop of decision 9.
- `e2e/ha_failover_test.go`: steps 9a to 9c.

## Consequences

- The server sets pool settings that pgx does not set by default. A URL can
  still set each one inside its range. A URL with a value outside the range
  stops the start, also a URL that worked before. No tag shipped, so no
  upgrade path is necessary.
- A URL with `target_session_attrs=any`, `read-only`, `standby` or
  `prefer-standby` stops the start.
- The readiness probe sends one more statement than a ping.
- A statement that runs for more than 30 seconds on an API path fails with
  503. A slow query then shows up as a 503 that names the cause in the log,
  and not as a request that hangs.
- Step 9 runs only on a runner with the PostgreSQL server binaries.
- The several-instances guide gets the effective defaults, the table of
  decision 8, the readiness check, the DNS advice and the measured figures
  of step 9. The configuration reference lists the settings and their ranges.
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
