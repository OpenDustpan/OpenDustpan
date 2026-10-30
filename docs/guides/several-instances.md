# Run several instances

This guide gives the rules for several `opendustpan serve` processes on one PostgreSQL database and one S3-compatible bucket.

This layout is not supported yet. The README gives the current status: the server runs on one node. The [architecture guide](architecture-and-sizing.md) shows the target design. Use this guide to prepare an installation for that design. Do not use several instances in an installation yet.

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

- `api` is ready when the store and the blob store answer.
- `worker` is ready when the store answers and its claim loop ran in the last 2 claim intervals. A claim interval is the longer of the poll period and the lease period.

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
- `GET /api/server/worker` of the installation API shows `silentSeconds` and lists each due schedule and each operation that no worker claimed for more than 2 lease periods. It needs the action `server:observe`.

Each worker records a heartbeat in the store when it claims work or renews a claim. The heartbeat holds the lease period of the worker. The `api` process reads the time of the store, not its own clock. It also reads the lease period of the worker, not its own lease period. Suppose no worker claimed or renewed a claim for 2 lease periods of the worker. Then the `api` process refuses each request that inserts a durable operation. It answers 503 with the error code `worker.absent`. These requests start a backup, start a restore, start a job, start a drift check, fire a trigger or approve an audit chain change. The error says: `start a worker with opendustpan serve --components worker`. Other requests stay as they are. A process that runs `all` or `worker` never refuses a start for this reason. An `api` process counts the 2 lease periods from the time it began to watch. So it never refuses a start right after it began. A start of a process never fails a restore that another instance runs.

`GET /api/server/components` lists the components of the process that answers.

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

The request needs a running instance. A binary below the minimum cannot write. When a dead instance blocks a migration, the new instance fails to start, and no instance answers the request. Start one instance of the old binary, at or above the current minimum. Delete the row, stop that instance, and start the new instance again.

These checks use the minimum:

- **Writes.** Each write of an instance compares the version of its binary with the highest minimum in the schema. A binary below the minimum fails the write in its own transaction. The Pulumi CLI, OpenTofu and the API get 503. The error code is `schema.binary-below-minimum`, and the text names both versions: `binary version v0.3.0 is below v0.4.0, the minimum binary version of the database schema, so this instance cannot write: stop this instance and run version v0.4.0 or later`. Reads still work. The row delete at a clean stop still works.
- **Migrations.** A migration that sets a new minimum does not run while a row below that minimum exists. The new instance fails to start with an error that names the migration, the new minimum and each row: `migration 0200_example.sql sets the minimum binary version v0.4.0 while 1 instance rows are below it: ins-4q2n7xk5b6d3m1p0a8wz (v0.3.0): stop these instances, or delete the row of an instance that no longer runs (DELETE /api/server/instances/{id}), and start again`. Stop the old instances, or delete the rows of the instances that no longer run, and start the new instance again.
- **Readiness.** An instance with a binary below the minimum answers 503 on the readiness probe. The `failed` list names the check `binary-minimum`, so a load balancer takes the instance out of the pool. The body holds no version text.
- **Restore.** A restore on a binary below the minimum fails when it starts and again in the swap. The error names both versions. The live data and the minimum stay as they were. Start the restore on an instance of the new binary.

A dev build passes every minimum check: the write check, the migration check, the readiness check and the restore check. A restore never replaces the rows, and a backup does not hold them.

## When the database does not answer

The database can stop, restart or fail over to a replica. While an instance cannot reach the database, it does these things:

- `/health/ready` answers 503. A load balancer with a health check on this route takes the instance out of its pool.
- Each API answers a request that needs the database with 503 and the header `Retry-After: 5`, not 500. The Pulumi Cloud-compatible API answers `{"code": 503, "message": "..."}`. The TFE-compatible API answers a JSON:API error. The other APIs answer their usual error body. The error code is `namespace.unavailable`.
- The instance does not restart. When the database answers again, `/health/ready` answers 200 and the instance serves requests again.

The adapters treat these failures as a database that does not answer:

- A failed connection.
- An error in the SQLSTATE class 08.
- The SQLSTATE codes 57P01, 57P02 and 57P03.
- A read-only database (25006).
- A SQLite database that stays busy after its busy timeout.

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

- OpenTofu: no state version stays pending, and the workspace has no lock.
- Pulumi: the update that the CLI could not complete stays running until its lease ends. A new `pulumi up` then answers 409 and names `pulumi cancel`. Run `pulumi cancel`, and run the command again. It completes.

Run the same command again after the database answers. The next `pulumi preview` and `tofu plan` show no changes.

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
