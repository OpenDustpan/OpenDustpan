# Run several instances

This guide gives the rules for several `opendustpan serve` processes on one PostgreSQL database and one S3-compatible bucket.

This layout is not supported yet. The README gives the current status: the server runs on one node. The [architecture guide](architecture-and-sizing.md) shows the target design. Use this guide to prepare an installation for that design. Do not use several instances in an installation yet.

## What each instance shares

Give each instance the same values for these settings.

| Setting | Rule |
| --- | --- |
| `DUSTPAN_DATABASE` | One PostgreSQL database. SQLite holds one instance only. |
| `--blob-store` (`DUSTPAN_BLOB_STORE`) | One S3-compatible bucket and prefix, `s3://<bucket>[/<prefix>]`. The `fs` store is on the disk of one host. The [S3 guide](s3-blobs.md) shows the endpoint, region and credential settings. |
| `--region` (`DUSTPAN_REGION`) | One region. |
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

Do not use `--generate-key-encryption-key` (`DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY`) on more than one instance. Each instance then makes its own key file on its own host. A secret that one instance encrypts can then not be read by the other instances.

When the database holds secrets, a start with a different key stops with an error that names the `rewrap` command. When the database holds no secrets yet, the start cannot compare the keys. Two keys then stay in use without an error.

## Bootstrap admin tokens

Give each instance the same tokens in the same order. The server gives the subject `admin` to the first token, `admin-2` to the second token, and so on. The server checks the holder of an OpenTofu lock by subject. With another order, one token gets another subject on each instance. A lock that one instance gave can then fail on another instance.

A change to the list needs a restart of each instance.

## Sign-in sessions

Console sessions live in the database. Each instance accepts a session that another instance made. No session setting exists.

Give each instance the same sign-in providers and client secrets. The browser returns from the identity provider to the URL in `--public-url`. The load balancer can send that request to any instance.

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

### No worker

With `api` processes only, nobody runs the background work. Four signs show that:

- `/health/ready/worker` of a `worker` process answers 503 when its claim loop stops.
- The metric `dustpan_worker_silent_seconds` shows the time since a worker last claimed work or renewed a claim.
- The metric `dustpan_oldest_unclaimed_operation_age_seconds` shows how long the oldest unclaimed operation or due schedule waits. It is 0 when none waits. It does not decide the refusal below. A long job, or a schedule that an older worker does not know, leaves work unclaimed while the workers run.
- `GET /api/server/worker` of the installation API shows `silentSeconds` and lists each due schedule and each operation that no worker claimed for more than 2 lease periods. It needs the action `server:observe`.

Each worker records a heartbeat in the store when it claims work or renews a claim. The time of the store counts, not the clock of the process that reads it. When no worker claimed or renewed a claim for 2 lease periods, an `api` process refuses each request that inserts a durable operation with 503. Today that is the approval of an audit chain change. The error says: `start a worker with opendustpan serve --components worker`. Other requests stay as they are. A process that runs `all` or `worker` never refuses a start for this reason. An `api` process counts the 2 lease periods from the time it began to watch. So it never refuses a start right after it began. A process that runs only `worker` does not fail the backups and the restores of the `api` processes when it starts.

`GET /api/server/components` lists the components of the process that answers.

A backup or a restore runs in the process that took the request, so it needs no worker. Send the request to an `api` process that has enough time and memory for it.

## Settings that stay on one host

Some targets write to the disk of the host that runs the instance. Each instance then holds a part of the data.

- A `dir:` backup target. Each instance writes its backups to its own disk. A restore on another instance does not find them. Use an S3-compatible backup target.
- A `file:` audit sink. Each instance writes only its own audit entries. Use a `blob:`, `syslog+tls://` or `webhook:` sink.
- A `file:` log sink. Each instance writes only its own log lines. Collect the files from each host, or use a `syslog+tls://` sink.

## Behind a load balancer

Set `--public-url` to the URL that clients use, for example `https://dustpan.example`. Without it, each instance makes absolute URLs from the host of each request. Console sign-in needs the public URL.

Set `--trusted-proxies` (`DUSTPAN_TRUSTED_PROXIES`) to the addresses of the load balancer. The server then reads the client address from the forwarding header in `--trusted-proxy-header`. Without it, the audit log and the IP allowlists see the address of the load balancer.

Send health checks to the readiness probe. The [observe guide](observe.md) gives the probe paths.

## Limits

- SQLite holds one instance only. Do not start two processes on one SQLite file.
- At start, an instance can mark a backup or a restore as failed while another instance runs it. Start or restart an instance only when no backup or restore runs.
