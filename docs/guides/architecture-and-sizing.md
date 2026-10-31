# Architecture and sizing

This guide shows one supported layout and one target design. It shows what runs where, what the parts share, and what stops when one part stops. It also holds the sizing tables. The [measured sizing page](measured-sizing.md) shows one load run of one to three instances.

## One instance with SQLite and a local blob directory

One `opendustpan serve` process forms the whole installation. It uses a SQLite file for the SQL store. It uses a local directory for the blob store. It uses one key-encryption key. It can write backups to a backup target.

### What runs where

- The server process runs on one machine. It serves the Pulumi Cloud-compatible API, the TFE-compatible API, the inventory API, and the installation API.
- The SQL store is a SQLite file on the same machine. There is no default path. Set the path with `--database` (`DUSTPAN_DATABASE`), for example `sqlite:/var/lib/opendustpan/dustpan.db`.
- The blob store is a directory on the same machine. The default path is `blobs` next to the SQLite file. It holds Pulumi checkpoints and OpenTofu state versions. Set another path with `--blob-dir` (`DUSTPAN_BLOB_DIR`).
- The key-encryption key comes from `DUSTPAN_KEY_ENCRYPTION_KEY` or from a key file. There is no default key file. The server makes no key.
- The backup target is optional. Set a directory or an S3-compatible bucket with `--backup-target` (`DUSTPAN_BACKUP_TARGET`). An empty target means the server takes no backup.

### What the parts share

One process holds every part. It alone owns the database file and the blob directory. No other process may open the SQLite file while the server runs.

SQLite runs in write-ahead log mode. It keeps `dustpan.db-wal` and `dustpan.db-shm` next to `dustpan.db`. Keep all three files on a local file system. SQLite write-ahead log mode does not work on a network file system.

### What stops when one part stops

- When the server process stops, every API stops. The Pulumi CLI, OpenTofu, and the console cannot reach any state.
- When the disk that holds the database file fails, all reads and writes fail.
- When the disk that holds the blob directory fails, checkpoint and state reads and writes fail.
- When you lose the key-encryption key, the server cannot open secret values or OpenTofu state. Nobody can recover them.

### How to back it up

Back up this layout the same way as any installation. The [backup guide](backup-restore-upgrade.md) shows the commands. Keep the key-encryption key apart from the backup target. A backup never holds the key.

## Several instances behind a load balancer with PostgreSQL and S3-compatible storage

This layout is supported. The [high availability guide](high-availability.md) gives the supported layout and its limits. Several `opendustpan serve` processes form one installation. A load balancer receives each request. It passes each request to one healthy instance. All instances share one PostgreSQL database, one S3-compatible bucket, one key-encryption key, and one backup target.

### What runs where

In this design:

- Each instance would run the same server binary with the same flags. Each instance would serve every API.
- The load balancer would receive each request. It would pass each request to one healthy instance.
- The SQL store would be one PostgreSQL database. Set it with `--database postgres://...` (`DUSTPAN_DATABASE`). When the URL holds a password, set it only in `DUSTPAN_DATABASE`, because `serve` refuses a password in the flag.
- The blob store would be one S3-compatible bucket. Set it with `--blob-store s3://<bucket>` (`DUSTPAN_BLOB_STORE`). The [S3 guide](s3-blobs.md) shows the endpoint, region, and credential settings.
- The key-encryption key would have the same value on each instance. Set it with `DUSTPAN_KEY_ENCRYPTION_KEY`. Keep it outside the database and the backup target.
- The backup target would have the same value on each instance. Set a directory or an S3-compatible bucket with `--backup-target` (`DUSTPAN_BACKUP_TARGET`).

### What the parts share

In the target design, each instance would share the SQL store, the blob store, the key-encryption key, and the backup target. Each instance would see the same namespaces, units, and history. No two instances would share a SQLite file. This layout would always use PostgreSQL.

### What stops when one part stops

In this design:

- If one instance stopped, the load balancer would pass requests to the healthy instances. Requests in flight to the stopped instance would fail.
- If PostgreSQL stopped, all reads and writes would fail on each instance.
- If the S3-compatible store stopped, checkpoint and state reads and writes would fail on each instance.
- If the load balancer stopped, no request would reach any instance.
- If you lost the key-encryption key, the server could not open secret values or OpenTofu state. Nobody could recover them.

### How to back it up

In the target design, you would back up this layout the same way as any installation. The [backup guide](backup-restore-upgrade.md) shows the commands. A backup would cover the whole installation. It would hold the SQL data with the blob content that the data names. Keep the key-encryption key apart from the backup target. A backup never holds the key.

## Sizing

Size each installation from a load run, not from guesses. The [measured sizing page](measured-sizing.md) gives the numbers of one run of one to three instances on one host. The [load harness guide](load.md) shows how to run the harness and how to read its report. Each table below gains one row per release, with the hardware and the date of the run.

### Memory per instance

| Release | Hardware | Date | Peak memory per instance |
| --- | --- | --- | --- |
| No measurement yet. The first release load run fills this table. | | | |

### CPU per instance

| Release | Hardware | Date | CPU per instance |
| --- | --- | --- | --- |
| No measurement yet. The first release load run fills this table. | | | |

### Database size

| Release | Hardware | Date | Database size |
| --- | --- | --- | --- |
| No measurement yet. The first release load run fills this table. | | | |

### Database connections

| Release | Hardware | Date | Database connections |
| --- | --- | --- | --- |
| No measurement yet. The first release load run fills this table. | | | |

### Blob storage per update

| Release | Hardware | Date | Blob bytes per update |
| --- | --- | --- | --- |
| No measurement yet. The first release load run fills this table. | | | |

### Largest checkpoint

| Release | Hardware | Date | Largest checkpoint tested |
| --- | --- | --- | --- |
| No measurement yet. The first release load run fills this table. | | | |

### Inventory writes in one namespace

Each write that changes the inventory of a namespace appends one row to the inventory change log. These writes wait for each other at the commit. Writes in different namespaces do not wait for each other.

A PostgreSQL benchmark on a machine with 4 CPUs and 32 clients measured approximately 1,170 such writes each second in one namespace. The extra cost of the row is approximately 0.14 ms for a write that does not wait. A write that changes only a lock appends no row.

### Units per size

| Release | Hardware | Date | Units tested |
| --- | --- | --- | --- |
| No measurement yet. The first release load run fills this table. | | | |

## Measure your own installation

Use the [load harness guide](load.md) to measure your own installation. It shows how to run the harness and how to read its report.

Run it with SQLite:

```sh
make load
```

Run it with PostgreSQL:

```sh
DUSTPAN_TEST_POSTGRES_URL='postgres://<user>:<password>@localhost:5432/dustpan_load' make load
```

The placeholders `<user>` and `<password>` stand for your database user and password. Never use a real password in a shared command.
