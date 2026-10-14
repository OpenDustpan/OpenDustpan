# Architecture and sizing

This guide shows the layout of an installation, what each part needs, and
how to measure the size that your installation needs. It follows ADR 40,
decision 8.

## Release numbers

No release exists yet, so this guide has no sizing tables yet. ADR 40 says
that the tables come from a load run on each release. Each table will name
the release, the hardware and the date. Until then, measure your own
installation with the load harness (see below).

## Architectures

### One instance with SQLite

This is the default. One `opendustpan serve` process uses:

- **The SQL store:** a SQLite file. The default is `dustpan.db` in the
  working directory.
- **The blob store:** a directory. The default is `blobs` next to the SQLite
  file. It holds the Pulumi checkpoints and the OpenTofu state.
- **The key-encryption key:** `DUSTPAN_KEY_ENCRYPTION_KEY`, or a key file.
  The default key file is `dustpan.key` next to the SQLite file.
- **A backup target** (optional): a directory or an S3-compatible bucket.

SQLite runs in write-ahead log (WAL) mode. Next to `dustpan.db`, it keeps
the files `dustpan.db-wal` and `dustpan.db-shm`. Keep the three files on a
local file system: SQLite WAL mode does not work on a network file system.

### One instance with PostgreSQL and S3-compatible storage

One `opendustpan serve` process uses:

- **The SQL store:** a PostgreSQL database, set with
  `--database postgres://...`.
- **The blob store:** a bucket, set with `--blob-store s3://<bucket>`. The
  [S3 guide](s3-blobs.md) shows the settings.
- **The key-encryption key:** as above. With PostgreSQL, the default key
  file is `dustpan.key` in the working directory.
- **A backup target** (optional): as above.

The server sets no limit on its PostgreSQL connections. It uses the default
of the Go `database/sql` package, which has no limit.

### Several instances

The server supports one node today. High availability is an open proposal
(RFC 36). The load harness runs two instances on one PostgreSQL database to
measure the database latency, but this is not a supported layout yet.

## Measure your own installation

The [load harness](load.md) starts its own server, replays recorded CLI
requests and writes a JSON report. Run it on the machine and the database
that you plan to use.

With SQLite:

```sh
make load
```

With PostgreSQL, point the URL at an empty database:

```sh
DUSTPAN_TEST_POSTGRES_URL='postgres://user:pass@localhost:5432/dustpan_load' make load
```

The report answers these sizing questions:

| Question | Report field | Notes |
| --- | --- | --- |
| Memory to save a Pulumi checkpoint | `checkpoints[].fullPeakAllocBytes`, `deltaPeakAllocBytes` | Peak heap of the server and the harness client together, at 256 KiB, 1 MiB and 4 MiB |
| Memory to import a Pulumi stack | `imports[].peakAllocBytes` | Same sizes and same limit as above |
| Time to save a checkpoint | `checkpoints[].fullMs`, `deltaMs` | Same sizes |
| Lock contention on one OpenTofu workspace | `workspaceLocks` | 16 workers, 8 rounds each |
| Many Pulumi updates in one namespace | `namespaceUpdates` | 64 updates at the same time |
| List latency with many units | `lists` | 10,000 units by default, half stacks and half workspaces |
| Database latency with two instances | `database` | PostgreSQL only |

The harness does not measure these items. Their values are unknown until a
run measures them:

- CPU for each instance.
- The database size and the blob storage that each update adds.
- The number of database connections.
- A Pulumi checkpoint larger than 4 MiB. The server accepts up to 32 MiB by
  default (`--max-checkpoint-bytes`).
- OpenTofu state larger than 32 MiB. The server accepts up to 32 MiB by default
  (`--max-state-bytes`). A higher value raises the memory of the call that reads the state. An upload with large outputs asks for more budget as the outputs grow, and answers 503 with `Retry-After: 1` when the budget has no room.
- More than one namespace, or more units than the run seeds (`-units`).
