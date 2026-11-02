# High availability on one database

This guide gives the supported high-availability layout of one installation. It says what to run, what the server does when a part stops, and what the operator must provide.
It is the entry point.
Other guides give the details, and this guide links to them.

OpenDustpan makes no service level agreement and no compliance claim.
A figure in this guide is a measurement with a date, not a promise.

## The supported layout

One installation runs as two or more `opendustpan serve` instances behind one load balancer.
All instances use one PostgreSQL database and one S3-compatible object store.
Each instance serves any request.
The instances have no leader and do not call each other.

```text
clients (Pulumi CLI, OpenTofu, console, API)
        |
  load balancer  -- health check: GET /health/ready
        |
  +-----+-----+-----+
  |     |     |     |
 serve serve serve ...   one installation, same settings
  |     |     |     |
  +-----+-----+-----+
        |           \
  PostgreSQL         S3-compatible store
  (database)         (blobs and backups)
```

The minimum topology is:

- Two or more instances of one installation.
- PostgreSQL 18 or later. The tests run on PostgreSQL 18 only.
- An S3-compatible store for the blobs.
- A backup target on an S3-compatible store.
- A load balancer with a health check on `/health/ready`.

You run the database and the object store, and you keep them available.
OpenDustpan does not replicate the database or the object store.

## Settings that all instances share

Give each instance the same database, blob store, installation name, key-encryption key, bootstrap admin tokens, backup target, public URL, sign-in providers and audit chain settings.
The [several instances guide](several-instances.md#what-each-instance-shares) lists each setting and the reason.
A start with a different key stops with an error, when the database holds secrets.

## Run it with Helm

Set `replicas` above 1.
The chart then changes these things:

- The Deployment uses `RollingUpdate` with `maxUnavailable` 0 and `maxSurge` 1.
- The chart makes a PodDisruptionBudget with `minAvailable` 1. The value must be less than `replicas`.
- A soft topology spread constraint prefers a different node for each pod (`spreadAcrossNodes`).
- A `preStop` hook waits `preStopSeconds` (15 by default) before the pod gets SIGTERM.
- The chart refuses `blobStore.type: fs`, `backup.target.type: dir`, a `file:` audit or log sink, and a `sqlite:` database, with a message that names the setting.

Set `worker.enabled: true` to run the background work in its own Deployment.
The chart file `deploy/helm/opendustpan/ci/ha.yaml` is a values file with 3 server replicas and 2 worker replicas.
See [Install with Helm](kubernetes.md#several-instances) and the [Helm values reference](../reference/helm-values.md).

If you do not use Helm, run the same layout with your own tools.
Use an S3-compatible blob store and backup target, and the shared settings above.

## Components

By default each instance runs all components.
`opendustpan serve --components api` runs the HTTP routes only.
`opendustpan serve --components worker` runs the background work only: durable operations, schedules, backups and restores, the audit shipper and the audit chain check.
Run at least one process that has the `worker` component.
Without it, some requests answer 503 with the error code `worker.absent`. These are the requests that start a backup, a restore, a job, a drift check, a trigger, or an audit chain approval.
See [Run the components in separate processes](several-instances.md#run-the-components-in-separate-processes).

## Readiness, drain and rolling deploys

Send the health check of the load balancer to `GET /health/ready`.
It answers 200 when the instance can serve and 503 when it cannot.
The body names each failed check.
`GET /health/live` answers 204 while the process runs.

When an instance gets SIGTERM, it does these steps:

1. It answers 503 on `/health/ready` at once, with the failed check `draining`.
2. A `worker` component gives back its claims, so another worker takes the work with no wait for the lease.
3. It serves other requests for a drain of 10 seconds. The drain has a fixed length and no flag.
4. It closes the listener and waits up to 30 seconds for the requests in flight.

Give the instance at least 50 seconds between SIGTERM and SIGKILL.
The Helm chart gives the pod 60 seconds, after a `preStop` wait of 15 seconds.

To deploy a new release, replace the instances one at a time.
Wait until the new instance is ready before you stop an old one.
The Helm chart does this with `RollingUpdate`.
A release can set a minimum binary version in the schema:

- An instance of an older binary does not write. Its writes fail with 503 and the error code `schema.binary-below-minimum`. Its reads work.
- Its readiness answers 503 with the failed check `binary-minimum`, so the load balancer leaves it out of the pool.
- A migration that sets a new minimum does not run while an instance row below it exists. The new instance does not start, and the error names each row.
- A restore on an older binary fails when it starts.

Each instance writes a row at start and deletes it at a clean stop.
`GET /api/server/instances` lists the rows.
`DELETE /api/server/instances/{id}` deletes the row of an instance that died.
See [Roll out a new release](several-instances.md#roll-out-a-new-release).

## When an instance stops

The load balancer takes the instance out of the pool when its health check fails.
A client request that was in flight on a killed instance fails, and the client sends it again to another instance.
The Pulumi CLI and OpenTofu retry a request that fails with 503.

The durable operations of the instance continue on another instance:

- A backup or a restore that a stopped instance ran continues on another `worker` process. After SIGTERM this takes place at once. After a kill it takes place when the lease ends. The lease is 30 seconds and has no setting.
- A restore continues from the step after the last recorded step.
- A schedule tick runs on one instance only.

A Pulumi update or an OpenTofu lock that a killed instance served is not lost, because the state is in the database and the store.
If a CLI command fails, run it again.
If `pulumi up` answers 409 and names `pulumi cancel`, run `pulumi cancel` and then run the command again.

## When the database does not answer

While an instance cannot reach the database:

- `/health/ready` answers 503.
- Each request that needs the database answers 503 with the header `Retry-After: 5` and the error code `namespace.unavailable`, not 500. The Pulumi Cloud-compatible API uses `{"code": 503, "message": "..."}`. The TFE-compatible API uses a JSON:API error.
- The instance does not restart. When the database answers again, the instance is ready again.

The Pulumi CLI retries one write for about 13 to 14 seconds before the command fails.
OpenTofu retried for more than 30 seconds in the e2e suite.
The [several instances guide](several-instances.md#how-long-a-cli-command-retries) gives the table of versions and the date of the measurement.
A command that fails in a longer outage leaves no half-written state version and no lock.

Use `target_session_attrs=read-write` and a short `pool_max_conn_lifetime` in the database URL.
See [Database settings for a failover](several-instances.md#database-settings-for-a-failover).
The failover time of the database depends on your service and its settings.
OpenDustpan makes no claim about it.

## When the object store does not answer

A request that needs a blob fails while the store does not answer.
`/health/ready` of the `api` component checks the blob store, so an instance whose store check fails leaves the pool.
OpenDustpan does not keep a second copy of a blob.
Keep the object store available.

## Limits that hold for all instances

These limits count in the database, so they hold for the whole installation, not for one instance:

- The sign-in failure budget.
- The limit of pending state versions of a workspace.
- The limits of the test of a notification destination.

Some limits count in the memory of one instance. For these, the limit of the installation is the limit of one instance times the number of instances.
The [several instances guide](several-instances.md#limits-that-count-in-each-instance) lists them.

## Backups and restores

A backup and a restore are durable operations.
The `api` process stores the request, and a `worker` process runs it and renews its claim.
Any instance answers the state, because the state is in the database.
Use an S3-compatible backup target.
A `dir:` target is on the disk of one host, and a restore on another instance does not find the backups.

While a backup or a restore runs, the server does not delete a blob that it can need.
The delete waits.
So a long backup keeps unused blobs in the store for longer.
See [Blob deletes wait for a backup or a restore](deleted-units.md#blob-deletes-wait-for-a-backup-or-a-restore).

A restore puts the installation in maintenance mode, and each instance refuses writes in its next request.
See [Back up, restore and upgrade](backup-restore-upgrade.md).

## What is not supported

- More than one instance on SQLite. SQLite holds one instance.
- Primary and replica installations, request forwarding, and replication between installations. These are a later milestone.
- Active-active across sites. One installation uses one database.
- The `fs` blob store, a `dir:` backup target and a `file:` audit or log sink with several instances. Each writes to the disk of one host. The Helm chart refuses them.
- PostgreSQL before version 18. Nobody tested it.
- A setting for the lease period. The lease is 30 seconds.
- Any promise about the recovery time of the database or the object store. These depend on the service you run.
- A claim that a named S3-compatible product works. OpenDustpan makes no such claim.

## Related guides

- [Run several instances](several-instances.md) gives the settings and the details.
- [Install with Helm](kubernetes.md) gives the chart.
- [Observe the server](observe.md) gives the probes and the metrics.
- [Architecture and sizing](architecture-and-sizing.md) gives the design.
- [Measured sizing](measured-sizing.md) gives the throughput, the latency, the CPU and the connections of one to three instances, and the error rate of a rolling restart.
