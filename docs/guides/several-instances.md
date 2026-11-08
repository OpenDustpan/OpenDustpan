# Run several instances

This guide gives the rules for several `opendustpan serve` processes on one PostgreSQL database and one S3-compatible bucket.

The [high availability guide](high-availability.md) gives the supported layout and what the server does when a part stops. Use this guide for the settings and the details. The [architecture guide](architecture-and-sizing.md) shows the design.

## What each instance shares

Give each instance the same values for these settings.

| Setting | Rule |
| --- | --- |
| `DUSTPAN_DATABASE` | One PostgreSQL database. SQLite holds one instance only. |
| `--blob-store` (`DUSTPAN_BLOB_STORE`) | One S3-compatible bucket and prefix, `s3://<bucket>[/<prefix>]`. The `fs` store is on the disk of one host. The [S3 guide](s3-blobs.md) shows the endpoint, region and credential settings. |
| `--installation` (`DUSTPAN_INSTALLATION`) | One installation name. |
| `DUSTPAN_KEY_ENCRYPTION_KEY` or `DUSTPAN_KEY_ENCRYPTION_KEY_FILE` | One key-encryption key. |
| `DUSTPAN_ADMIN_TOKEN` or `DUSTPAN_ADMIN_TOKEN_FILE` | One list of bootstrap admin tokens, in the same order. |
| `--backup-target` (`DUSTPAN_BACKUP_TARGET`) | One S3-compatible bucket, `s3://<bucket>[/<prefix>]`. |
| `--public-url` (`DUSTPAN_PUBLIC_URL`) | The URL of the load balancer. |
| `--oidc-*` settings and `DUSTPAN_OIDC_CLIENT_SECRET` | One set of sign-in providers. |
| `--audit-chain`, `--audit-signing-key-file`, `--audit-verify-keys-file` | One audit chain setting and one signing key. |

The [configuration reference](../reference/configuration.md) gives each setting.

## The key-encryption key

Make the key one time. Give the same value to each instance.

```sh
openssl rand -base64 32
```

Set the value in `DUSTPAN_KEY_ENCRYPTION_KEY`, or in a file that `DUSTPAN_KEY_ENCRYPTION_KEY_FILE` names on each host. Keep the key outside the database and the backup target.

Give each instance the same key. The server makes no key, so an instance with a different key file cannot read a secret that another instance encrypts.

When the database holds secrets, a start with a different key stops with an error that names the `rewrap` command. When the database holds no secrets yet, the start cannot compare the keys. Two keys then stay in use without an error.

## Bootstrap admin tokens

Give each instance the same tokens in the same order. The server gives the subject `admin` to the first token, `admin-2` to the second token, and so on. The server checks the holder of an OpenTofu lock by subject. With another order, one token gets another subject on each instance. A lock that one instance gave can then fail on another instance.

A change to the list needs a restart of each instance.

## Sign-in sessions

Console sessions live in the database. Each instance accepts a session that another instance made. No session setting exists.

Give each instance the same sign-in providers and client secrets. The browser returns from the identity provider to the URL in `--public-url`. The load balancer can send that request to any instance.

## The public URL

Every instance serves the same public URL (`--public-url`).

The Pulumi CLI records the server URL in the secrets-manager configuration of each stack when the stack is made. A stack cannot read its secrets through a different URL.

To change the URL, use one of these methods:

- Keep the old host name as an alias at the proxy.
- Run `pulumi stack change-secrets-provider` for each stack. This may need the old URL to stay reachable while it runs.

## Run the components in separate processes

A process runs one or more components. The `--components` flag (`DUSTPAN_COMPONENTS`) selects them. The default is `all`.

| Component | What it runs |
| --- | --- |
| `api` | Each HTTP route: the Pulumi Cloud-compatible API, the TFE-compatible API, the tool-neutral APIs, the installation API and the console files. It starts a durable operation, but it never claims one. |
| `worker` | The durable operations, the job schedules, the audit shipper and the audit chain check. |

```sh
opendustpan serve --components api      # scale this for HTTP capacity
opendustpan serve --components worker   # scale this for background work
```

An unknown name stops the start. The error names the valid components: `all`, `api` and `worker`.

Each process, whatever its components, runs the store, the key-encryption key check, `/health/live`, `/health/ready` and the metrics route. Each process migrates the database at start, under a lock, so the start order of the processes does not matter. A process that runs `worker` without `api` listens on its port for the probes and the metrics route only. Each other route answers 404. Send client traffic only to `api` processes.

The module switches of the installation apply to each component. The capabilities document lists modules, not components.

### Flags that a component does not read

Some flags matter to the `api` component only. For example, `--console-dir`, `--public-url` and `--oidc-*` matter to `api`. A process that runs `worker` without `api` ignores them, and it does not bind the sign-in providers. The `worker` component reads the other flags too. For example, `--allowed-origins` sets the CORS rule of the metrics route that a `worker` process serves. `--trusted-proxies` and `--trusted-proxy-header` set the client address in its request log. `--soft-delete-retention` sets the end of the retention that `api` records and that `worker` deletes at. `--audit-sink` names the sinks that `api` lists and `worker` ships to. Give these flags to each process. The start log has one line for each such flag, `setting has no effect: no selected component reads it`. The flag does not stop the start. Give each process the same settings if you want one configuration for all processes.

### Readiness

`/health/ready` answers for the components of the process and lists each one with its state in the field `components`. The fields `status` and `failed` keep their meaning: `failed` names each check that failed, and is absent when none failed. `/health/ready/api` and `/health/ready/worker` answer for one component. A component that the process does not run answers 404.

- `api` is ready when the store and the blob store answer, and the PostgreSQL database accepts writes.
- `worker` is ready when the store answers, the PostgreSQL database accepts writes, and its claim loop ran in the last 2 claim intervals. A claim interval is the longer of the poll period and the lease period.

The state of the audit chain is information only. While a change of the audit chain waits for approval, the body of the `api` component shows `"audit-chain": "waiting"` in the field `info`. The probe still answers 200. The approval needs the `api` processes in the pool of the Service. The Helm chart probes `/health/ready`.

`/health/live` is the same for each component: it answers when the process runs, and it does not check the claim loop.

### Stop an instance

When an instance gets SIGTERM, it does these steps in this order:

1. `/health/ready` and `/health/ready/{component}` answer 503 at once. The body names the failed check `draining`. `/health/live` still answers 204.
2. The `worker` component gives back the claim of each durable operation that it runs, and it stops the work. Another `worker` process then claims the operation at its next poll, with no wait for the end of the lease. The give-back checks the claim token in the database. It never gives back a claim that another instance took.
3. The instance keeps serving all other requests for a drain of 10 seconds. A load balancer with a health check on `/health/ready` takes the instance out of its pool in this time. The drain has a fixed length and no flag.
4. The instance closes the listener and waits at most 30 seconds for the requests in flight. A request that began before the signal gets its answer.

The drain and the wait together are at most 40 seconds. The Helm chart waits 15 seconds in `preStop` before the signal, and it gives the pod 60 seconds. If you run the instance in another way, give it at least 50 seconds between SIGTERM and SIGKILL. The drain and the wait take 40 seconds, and 10 seconds are margin.

### No worker

With `api` processes only, nobody runs the background work. Four signs show that:

- `/health/ready/worker` of a `worker` process answers 503 when its claim loop stops.
- The metric `dustpan_worker_silent_seconds` shows the time since a worker last claimed work or renewed a claim.
- The metric `dustpan_oldest_unclaimed_operation_age_seconds` shows how long the oldest unclaimed operation or due schedule waits. It is 0 when none waits. It does not decide the refusal below. A long job, or a schedule that an older worker does not know, leaves work unclaimed while the workers run.
- `GET /api/dustpan/v1/worker` of the installation API shows `silentSeconds` and lists each due schedule and each operation that no worker claimed for more than 2 lease periods. It needs the action `server:observe`.

Each worker records a heartbeat in the store when it claims work or renews a claim. The heartbeat holds the lease period of the worker. The `api` process reads the time of the store, not its own clock. It also reads the lease period of the worker, not its own lease period. Suppose no worker claimed or renewed a claim for 2 lease periods of the worker. Then the `api` process refuses each request that inserts a durable operation. It answers 503 with the error code `worker.absent`. These requests start a backup, start a restore, start a job, start a drift check, fire a trigger or approve an audit chain change. The error says: `start a worker with opendustpan serve --components worker`. Other requests stay as they are. A process that runs `all` or `worker` never refuses a start for this reason. An `api` process counts the 2 lease periods from the time it began to watch. So it never refuses a start right after it began. A start of a process never fails a restore that another instance runs. A start that sends an existing Idempotency-Key also needs a worker. When no worker runs, the server answers 503 with `worker.absent` and does not replay the first answer.

`GET /api/dustpan/v1/components` lists the components of the process that answers.

A restore is a durable operation too. The `api` process that took the request stores it, and a `worker` process runs its steps. The worker loads the backup into a staging area and checks it. Then it enters maintenance mode, takes the backup of the live data and copies the blobs. Then it swaps the staged data into the live data, leaves maintenance mode and drops the staging area. Each step records its result in the database. When the instance that runs a restore stops, another `worker` process continues the restore after the lease ends, from the step after the last recorded step. Run at least one `worker` process with enough time and memory for a restore, or the restore waits. Any instance answers the state of a restore, and any instance takes a cancel before the swap.

A backup is a durable operation. The `api` process that took the request stores it. A `worker` process runs it and renews its claim while it works. A start, a restart or a rolling update of an instance never fails a backup that another instance runs. When the instance that runs a backup stops with SIGTERM, it gives the claim back, and another `worker` process continues the backup at once. When the instance dies with no clean stop, another `worker` process continues the backup after the lease ends. The lease is 30 seconds by default. Run at least one `worker` process, or the backup waits. Any instance answers the state of a backup, because the state is in the database.

## Roll out a new release

You can replace the instances of one installation one at a time. A release can set a minimum binary version in the schema. An instance with a binary below the minimum must stop writing, because the schema no longer holds what that binary needs.

Each instance writes one row at start-up: its instance ID, the version of its binary and its start time. A clean stop deletes the row. The row is not a lease. Nothing renews it, and nothing reads it to decide that an instance is live. An instance that dies without a clean stop leaves its row.

`GET /api/server/instances` lists the rows, oldest start first. It needs the action `server:read-instances`.

```json
{"instances": [{"id": "ins-4q2n7xk5b6d3m1p0a8wz", "binaryVersion": "v0.3.0", "startedAt": "2026-10-20T08:00:00Z"}]}
```

`DELETE /api/server/instances/{id}` deletes one row and answers 204. It needs the action `server:delete-instance`. It writes one entry in the audit log of the installation (the scope `@server`), with the action and the ID in the value path `/instances/{id}`. It does not stop the instance. Use it for the row of an instance that died.

The request needs a running instance. A binary below the minimum cannot write. An administrator can still delete an instance row with `DELETE /api/server/instances/{id}` through an instance that runs a binary below the minimum. When a dead instance blocks a migration, the new instance fails to start, and no instance answers the request. Start one instance of the old binary, at or above the current minimum. Delete the row, stop that instance, and start the new instance again.

These checks use the minimum:

- **Writes.** Each write of an instance compares the version of its binary with the highest minimum in the schema. A binary below the minimum fails the write in its own transaction. The Pulumi CLI, OpenTofu and the API get 503. The error code is `schema.binary-below-minimum`, and the text names both versions: `binary version v0.3.0 is below v0.4.0, the minimum binary version of the database schema, so this instance cannot write: stop this instance and run version v0.4.0 or later`. Reads still work. The row delete at a clean stop still works.
- **Background work.** An instance with a binary below the minimum does not claim or run background work. These calls fail with the same error:
  - the start, the claim, the renewal, the steps and the end of a durable operation.
  - the schedules and their ticks, and the heartbeat.
  - the start and the end of a backup.
  - the change of the installation state.
  - the audit chain and the shipping of an audit sink.
  - the registration of the instance.
  - the count of sign-in failures.

  A refused claim leaves the operation unclaimed, so an instance of a new binary takes it. These calls stay open, so an old instance can stop cleanly. It deletes its own row, hands back its claims and the lease of an audit sink, and appends audit entries. The revocation of a token, a session or a grant stays open too.
- **Migrations.** A migration that sets a new minimum does not run while a row below that minimum exists. The new instance fails to start with an error that names the migration, the new minimum and each row: `migration 0200_example.sql sets the minimum binary version v0.4.0 while 1 instance rows are below it: ins-4q2n7xk5b6d3m1p0a8wz (v0.3.0): stop these instances, or delete the row of an instance that no longer runs (DELETE /api/server/instances/{id}), and start again`. Stop the old instances, or delete the rows of the instances that no longer run, and start the new instance again.
- **Readiness.** An instance with a binary below the minimum answers 503 on the readiness probe. The `failed` list names the check `binary-minimum`, so a load balancer takes the instance out of the pool. The body holds no version text.
- **Restore.** A restore on a binary below the minimum fails when it starts and again in the swap. The error names both versions. The live data and the minimum stay as they were. Start the restore on an instance of the new binary.

A dev build passes every minimum check: the write check, the migration check, the readiness check and the restore check. A restore never replaces the rows, and a backup does not hold them.

## When the database does not answer

The database can stop, restart or fail over to a replica. While an instance cannot reach the database, it does these things:

- `/health/ready` answers 503. A load balancer with a health check on this route takes the instance out of its pool. When the database answers but refuses writes, for example an old primary after a failover, the `failed` list names the check `store-read-only`.
- Each API answers a request that needs the database with 503 and the header `Retry-After: 5`, not 500. The Pulumi Cloud-compatible API answers `{"code": 503, "message": "..."}`. The TFE-compatible API answers a JSON:API error. The other APIs answer their usual error body. The error code is `namespace.unavailable`.
- The instance does not restart. When the database answers again, `/health/ready` answers 200 and the instance serves requests again.

The adapters treat these failures as a database that does not answer:

- A failed connection.
- The SQLSTATE codes 08000, 08001, 08003, 08004, 08006 and 08007 (a connection exception).
- The SQLSTATE codes 57P01, 57P02 and 57P03 (the database server stops, crashes or does not accept connections yet).
- The SQLSTATE code 25006 (a read-only database, for example after a failover to a replica).
- The SQLSTATE code 57014 (the server ended a statement, for example at its statement timeout).
- A round trip to the database that takes longer than 30 seconds. The server then closes the connection. A host can stop answering and keep the connection open. The request then ends after 30 seconds, not after the TCP timeout of the host.
- The SQLSTATE code 53300 (too many connections). A session that ends frees a connection, so a new try can work.
- A SQLite database that stays busy after its busy timeout.

Other errors of the database stay errors with status 500. For example, 08P01 (protocol violation) is a bug. The codes 53200 (out of memory) and 53100 (disk full) are server faults. A new try is likely to meet them again.

### How long a CLI command retries

The Pulumi CLI and OpenTofu retry a request that gets 503. The retry has a limit. A database outage that is longer than the limit makes the running command fail. The command fails with a message that has the status 503.

The e2e suite measured these times on 2026-10-28. For each version, the time is how long the CLI sent the same write again before the command failed. The suite measured them through its own proxy. The proxy answers 503 with `Retry-After: 1` when no instance is ready. The times are measurements. They are not a guarantee, and they may differ for another version, another proxy or another load.

| CLI version | Time that it retried one write |
| --- | --- |
| Pulumi 3.200.0 | 14 s |
| Pulumi 3.250.0 | 13 s |
| Pulumi 3.272.0 | 14 s |
| OpenTofu 1.11.14 | More than 30 s. The command did not fail in an outage of 60 s. It completed 6 s after the database came back. |
| OpenTofu 1.12.7 | More than 30 s. The command did not fail in an outage of 60 s. It completed 7 s after the database came back. |
| OpenTofu 1.13.2 | More than 30 s. The command did not fail in an outage of 60 s. It completed 6 s after the database came back. |

In the same suite, an outage of 7 s to 8 s did not stop `pulumi up` or `tofu apply`. Both commands completed with exit 0.

A database failover that is longer than these times makes running CLI commands fail. A command that failed leaves the state as it was before the write:

- OpenTofu: no state version stays pending, and the workspace has no lock. The one exception is a lost answer to a lock through the TFE-compatible API. See the next section.
- Pulumi: the update that the CLI could not complete stays running until its lease ends. A new `pulumi up` then answers 409 and names `pulumi cancel`. Run `pulumi cancel`, and run the command again. It completes.

Run the same command again after the database answers. The next `pulumi preview` and `tofu plan` show no changes.

### A lost answer to an OpenTofu lock request

A write can commit while its answer does not get to the CLI. OpenTofu then sends the same request again. The server keeps a record of the last released lock of each workspace. The record holds the lock ID, the holder and the principal that released the lock. It also holds the time of the release and the kind of release: normal or forced. The server uses the record for 24 hours after the release.

The unit list and the unit view of the inventory API show the record in the `lastRelease` field. Use it to see why a repeated unlock got success. The TFE-compatible API does not show it.

These repeated requests succeed and change nothing:

- A `LOCK` of the HTTP backend with the same lock ID from the same holder, while the holder still has that lock.
- An `UNLOCK` of the HTTP backend with the lock ID of the record, from the holder, after a normal release.
- An unlock through the TFE-compatible API (`actions/unlock`) from the holder of the record, after a normal release.
- A force-unlock from the principal that forced the release of the record. For the HTTP backend, this is an `UNLOCK` with the lock ID of the record. For the TFE-compatible API, this is `actions/force-unlock`.

A repeated request writes no audit entry, because nothing changes.

After a forced release, an unlock from the original holder gets 409. The holder and the CLI see that another principal released the lock.

One case stays 409. A lock through the TFE-compatible API (`actions/lock`) that OpenTofu sends again gets 409, because the workspace has a lock. That protocol sends no lock ID, so the server cannot tell a repeat from a second run. OpenTofu waits for the time of `-lock-timeout`, and then the command fails. The lock stays. The workspace shows the holder and the time of the lock. Make sure that no other run uses the workspace, and then run `tofu force-unlock <lock id>`.

## Database settings for a failover

After a failover, the old primary can stay up as a read-only database. A pool connection to it is open, but each write on it fails with the SQLSTATE code 25006. The server uses these settings of the `DUSTPAN_DATABASE` URL. When the URL does not name a setting, the server sets the default below:

| Setting | Default | Allowed values | What it does |
| --- | --- | --- | --- |
| `target_session_attrs` | `read-write` | `read-write`, `primary` | A new connection must reach a server that is not in recovery and whose transactions are not read-only. The server applies this check for both values: `primary` gets the same check as `read-write`. |
| `connect_timeout` | `5` | Whole seconds from 1 to 60 | The time limit to open one connection. |
| `pool_max_conn_lifetime` | `30m` | `1m` to `24h` | The pool closes a connection when it is older than this time, and opens a new one. |
| `pool_max_conn_lifetime_jitter` | `1m` | `0s` to the lifetime | A random part of the lifetime, so the instances do not open their connections again at the same time. |
| `pool_health_check_period` | `10s` | `1s` to `10m` | The pool checks the idle connections with this period and replaces a connection that is closed. |

The server also sets TCP keepalive on each connection: 15 seconds idle, 5 seconds between probes, and 3 probes.

A duration is a number with a unit, for example `5m` or `30s`. A value outside the allowed values stops the start of the server with the error code `store.failover-setting-invalid`. The error names the setting and the allowed values. The server does not change a value to make it valid. A setting that appears two times in the URL also stops the start. The variables `PGTARGETSESSIONATTRS` and `PGCONNECT_TIMEOUT` of the environment do not apply: the URL or the default decides.

At the start, the server writes the effective value of each setting to its log, with the word `url` or `default` for where the value came from. The log also gives the 30-second limit of each round trip.

Example:

```
DUSTPAN_DATABASE='postgres://dustpan:<password>@db.example.com:5432/dustpan?sslmode=verify-full&pool_max_conn_lifetime=10m'
```

The defaults are values that the project chose. Nobody measured them against a managed database.

The server also does these things to leave a read-only server:

- **A write that fails with 25006.** The connection reached a server that refuses writes. So the server closes all connections of the pool. A connection in use closes when its request ends. New connections must pass the check of `target_session_attrs`. The server closes the pool at most one time in 5 seconds. Each time, it writes a warning to its log with the reason `read-only`.
- **A 25006 in a read-only transaction of the server.** This can be a defect of the server, not a read-only database. So the server first checks its idle connections. It closes the pool only when one of them reaches a server that refuses writes.
- **The readiness probe.** The check of the store reads whether the database accepts writes, in one statement. When the database refuses writes, the probe answers 503 with the check `store-read-only`. The server then closes the connections of the pool in the same way. When the check cannot run, the probe answers 503 with the check `store`.

The server also sets the TCP keepalive of each connection. It sends a keepalive after 15 seconds without traffic, then every 5 seconds. After 3 keepalives with no answer, the connection closes. So an idle connection to a host that stopped answering closes in about 30 seconds.

### Server timeouts

The server sets these values on each connection to the database:

| Setting of the database session | Value | What it does |
| --- | --- | --- |
| `statement_timeout` | 25 seconds | The database ends a statement that runs for longer. The request gets 503 with `Retry-After: 5`, and the log names the cause. The connection stays in the pool. |
| `idle_in_transaction_session_timeout` | 60 seconds | The database ends a session that stays in a transaction with no statement for longer, and frees its locks. |
| `client_connection_check_interval` | 5 seconds | The database checks that the client is still there while a statement runs, and stops the statement of a client that went away. |

The URL cannot change these values. They act before the 30-second limit of each round trip. So a slow statement on a database that answers ends on the database, and its locks go free.

Some work runs for longer than these values. It has its own limit for each statement, and for the time in a transaction with no statement:

| Work | Statement limit | Limit with no statement |
| --- | --- | --- |
| A backup, a restore, and the copy of the rows of a backup | 1 hour | 15 minutes |
| The migrations at the start | 15 minutes | 15 minutes |
| The creation and the removal of a staging schema | 10 minutes | 1 minute |

This work sets its limits for its own transaction only. The work then gives its connection back to the pool. The server resets the session of that connection (`RESET ALL`). So the next request gets the values of the table above. The reset has a limit of 2 seconds. When the reset fails, the server closes the connection.

When a connection breaks, the driver closes it in the background. It first sends a cancel request on a new connection, which `connect_timeout` limits. A request can meet the 30-second limit. Its connection can then keep its slot of the pool for up to 15 seconds longer, while the driver closes the connection.

### What was tested

The tests ran on 2026-11-01 against PostgreSQL 18.6 on one host, with pgx 5.11.0 as the driver:

- Each setting above gets its default when the URL does not name it. It gets the value of the URL when the value is inside its range. A value outside its range stops the start, and the error holds no part of the URL besides the value.
- The server answers 503 with `Retry-After: 5` and the error code `namespace.unavailable` for each write while a proxy cuts all its database connections. After the proxy lets the connections through again, the same server accepts writes. Nobody restarted it.
- A session that the database ended (`pg_terminate_backend`) gives the same 503 answer.
- When the database turns read-only (`default_transaction_read_only = on`) and its sessions end, the server does not open a new read-only session. Reads and writes then answer 503 until the database accepts writes.
- With the default settings, the server does not open against a read-only database. It opens against the same database when the database accepts writes.
- A proxy held all bytes of a connection and closed nothing. Each kind of round trip then ended with 503 within the 30-second limit. The kinds are a statement, a read of rows, a batch, a copy, a savepoint, a commit and a rollback. The test used a shorter limit to run fast.
- A statement that runs for longer than the statement timeout of the database answers 503, and the next statement on the same connection works.
- Work with its own limits gets them inside its transaction. The next request on the same connection gets the values of the server again. A value that the work set for its session does not stay.
- The reset of a connection to a host that stopped answering ended in about 2 seconds, and the pool had a free slot again.
- A pool whose sessions are read-only closes its connections after one write fails with 25006. This is also true for a pool of one connection that the failed request still holds. A burst of such failures closes it only one time. A write in a read-only transaction of the server does not close a pool of writable connections.

### What should work, and was not tested

No test ran on these systems:

- A managed database service.
- A real promotion of a standby.
- A proxy that pools connections.
- A URL with more than one host.

The notes below follow from how the services are documented to work. They are not results. Read the documentation of your service for the failover time, and test a failover before you rely on it.

A URL can name more than one host, separated by commas. With `target_session_attrs=read-write`, the driver should try the hosts in order and use the first one that accepts writes. This is how the driver documents it. Nobody tested it here. Use this form only when the database service does not give one address that always names the primary.

| Service | Address to use | What the vendor page says | Vendor page |
| --- | --- | --- | --- |
| Amazon RDS Multi-AZ | The endpoint name of the DB instance | The failover changes the DNS record of the DB instance to point to the standby. You must open the existing connections again. | [Failover process for Multi-AZ DB instances](https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/Concepts.MultiAZ.Failover.html) |
| Google Cloud SQL, high availability | The IP address or the connection string of the instance | The standby shares the static IP address of the primary. The failover closes the existing connections. | [High availability](https://docs.cloud.google.com/sql/docs/postgres/high-availability) |
| Azure Database for PostgreSQL flexible server, zone-redundant high availability | The server host name | Clients connect to the host name of the primary server. The failover updates the DNS record of that name to the IP address of the standby. | [High availability](https://learn.microsoft.com/en-us/azure/postgresql/high-availability/concepts-high-availability) |
| Patroni | The address of a proxy or a load balancer that sends traffic only to the leader. Use `GET /primary` of the Patroni REST API as its health check. | `GET /primary` answers 200 only on the node that runs as the primary with the leader lock. The page does not say what happens to open connections. | [REST API](https://patroni.readthedocs.io/en/latest/rest_api.html) |
| CloudNativePG | The `rw` service of the cluster. Its name is `<cluster name>-rw`. | The `rw` service points to the primary instance. The `ro` service points to the replicas. The `r` service points to any instance of the cluster, the primary included. Use `rw`. Not checked against the vendor page on the documentation site: the check read the source file of the documentation in the project repository. | [Service management (source file)](https://github.com/cloudnative-pg/cloudnative-pg/blob/main/docs/src/service_management.md) |

The "What the vendor page says" column is a summary of the page on 2026-10-30. It is not a test result. Do not use the address of one node or a read-only service.

For each service, keep the default `target_session_attrs=read-write`. A server that is not the primary then cannot hold a connection of the pool.

During the failover, the instances answer 503 on the readiness probe. They also answer 503 on each request that needs the database. Section "When the database does not answer" describes this. This guide makes no claim about the failover time or about data loss. Both depend on the service and on its settings for synchronous replication.

## Settings that stay on one host

Some targets write to the disk of the host that runs the instance. Each instance then holds a part of the data.

- A `dir:` backup target. Each instance writes its backups to its own disk. A restore on another instance does not find them. Use an S3-compatible backup target.
- A `file:` audit sink. Each instance writes only its own audit entries. Use a `blob:`, `syslog+tls://` or `webhook:` sink.
- A `file:` log sink. Each instance writes only its own log lines. Collect the files from each host, or use a `syslog+tls://` sink.

## Behind a load balancer

Set `--public-url` to the URL that clients use, for example `https://dustpan.example`. Without it, each instance makes absolute URLs from the host of each request. Console sign-in needs the public URL.

Set `--trusted-proxies` (`DUSTPAN_TRUSTED_PROXIES`) to the addresses of the load balancer. The server then reads the client address from the forwarding header in `--trusted-proxy-header`. Without it, the audit log and the IP allowlists see the address of the load balancer.

Send health checks to the readiness probe. The [observe guide](observe.md) gives the probe paths.

## Limits that count in each instance

These limits count in the memory of one instance. The limit of the installation is the limit of one instance times the number of instances.

| Limit | What it bounds | Effect with several instances |
| --- | --- | --- |
| `--max-body-bytes-in-flight` | The memory that large request bodies hold at one time. | Each instance keeps its own budget. This is the intended effect: the limit protects the memory of one process. |
| Audit entries of refused requests | One audit entry for each refused action, object kind and scope in each minute, with a count of the others. | Each instance writes its own first entry for a key in a minute. The entries of the same denial can be as many as the instances. |
| Checks of a Git host connection | One check of a connection in 10 seconds, and 30 checks of an installation of the Git host in 1 minute. | Each instance applies the limits. The checks that the Git host receives can be as many as the instances times the limit. |

Three limits count in the database, so they hold for all instances. They are the sign-in failure budget, the limit of pending state versions of a workspace, and the limits of the test of a notification destination.

## Limits

- SQLite holds one instance only. Do not start two processes on one SQLite file.
