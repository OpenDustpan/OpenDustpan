# Backup, restore and upgrade

This guide shows how to back up a whole installation, restore it, and upgrade the server. It follows ADR 30 and ADR 33. The end-to-end suite ran each command in this guide.

A backup covers the whole installation. It holds the SQL data with the blob content that the data names. A backup holds no key-encryption key.

## Back up the installation

Set a backup target on the server. Use the flag or the variable. An empty target means the server takes no backup.

```sh
./bin/opendustpan serve --backup-target dir:/var/backups/dustpan
```

```sh
DUSTPAN_BACKUP_TARGET=dir:/var/backups/dustpan ./bin/opendustpan serve
```

The target holds `blobs/<key>`, `backups/<id>/db`, and `backups/<id>/manifest.json`. The server writes the manifest last. A backup holds OpenTofu state in plain text. Protect the target. Use a directory that only the server owner can read.

Start a backup with the backup command. It calls the server and waits until the backup leaves the running state. The token comes from `DUSTPAN_ADMIN_TOKEN` or from the file in `DUSTPAN_ADMIN_TOKEN_FILE`. It never comes from a flag.

```sh
opendustpan backup --server https://dustpan.example --ca-file ca.pem
```

The command prints the backup ID, the state, and the SQL size. It then prints two warnings. A backup cannot decrypt secrets without `DUSTPAN_KEY_ENCRYPTION_KEY`. A backup holds OpenTofu state in plain text, so protect the target. Only one backup runs at a time. A second start while one runs answers 409.

Keep `DUSTPAN_KEY_ENCRYPTION_KEY` apart from the backup target. A backup never holds the key. A lost key still means lost secrets. Without the key the server cannot open secret values after a restore.

The target can also be S3-compatible storage. Set the credentials in `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` and, for temporary credentials, `AWS_SESSION_TOKEN`. Set `--s3-endpoint` for a server other than AWS.

```sh
./bin/opendustpan serve --backup-target s3://dustpan-backups/prod --s3-endpoint https://s3.example
```

An S3-compatible target holds one object with a limit of 5 GiB. A SQL part above this limit fails with a clear error.

## Check a backup

Check a backup with the check command. It reads only the target. It needs no server, no database, and no key.

```sh
opendustpan backup check --backup bkp-aaa --backup-target dir:/var/backups/dustpan
```

A complete backup prints `backup bkp-aaa is complete` and exits with status 0. A broken backup exits with status 1 and names the first missing object by key.

## Restore a backup

Restore loads a backup into an empty installation. Stop the server first. Restore is an offline command with no REST route. Use the same key, the same dialect, and the same schema version as the backup source.

Empty the destination before the restore. For SQLite, delete the database file with its journals and delete the blob directory. For PostgreSQL, drop the schema and create it again empty, and delete the blob directory.

Run the restore command. It opens the destination store, reads the manifest, checks the key, copies the blobs, loads the SQL data, and compares the row counts.

```sh
opendustpan restore --from dir:/var/backups/dustpan --backup bkp-aaa --database sqlite:/var/data/dustpan.db --blob-dir /var/data/blobs --region local
```

To restore from an S3-compatible target, pass `--from s3://<bucket>[/<prefix>]` with `--s3-endpoint` and `--s3-region` as for `serve`. To load the blobs into an S3-compatible live store, pass `--blob-store s3://<bucket>[/<prefix>]` in place of `--blob-dir`. The command reads the S3 credentials from `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` and `AWS_SESSION_TOKEN` only.

The command prints the steps with the row count of each table. It ends with `restored backup bkp-aaa`. Then start the server on the restored data and read back state with both tools.

The command refuses a destination that is not empty. It writes nothing in that case. Use a new `--database` with a new `--blob-dir` and run the command again.

The command checks the key before it writes anything. With no key or with another key it stops and names `DUSTPAN_KEY_ENCRYPTION_KEY`. A restore with a wrong key leaves an empty database. It never leaves a database that names missing blobs.

The command refuses a backup of another dialect or another schema version. The error names both versions. Restore into the same dialect and the same schema version only. To change version, upgrade the server instead.

The command refuses a backup with no manifest or with an unknown format number. The manifest is internal in this milestone.

Restore keeps the home region and the failover version of each namespace. It warns when the `--region` flag is not among the regions of the manifest. The warning names the flag value and the backup regions.

## Upgrade the server

Run the upgrade check before an upgrade. It reads the database and writes nothing. It needs no key and starts no server.

```sh
opendustpan upgrade check --database sqlite:/var/data/dustpan.db
```

The command prints the binary version, the schema version of the database, the schema version that the binary needs, and the names of pending migrations. It ends with one verdict: `up to date`, `upgrade possible`, or `cannot start`. The exit code is 0 for the first two verdicts and 1 for the third and for errors. When migrations are pending, it says to back up the database first and that no downgrade path exists.

Back up the database before each upgrade. `serve` applies pending migrations at start.

For several nodes, start one new node first. The first new node migrates the database. An older node that still runs does not stop. Its next start fails because the schema is newer than the binary knows. Then start the other new nodes.

The project supports no downgrade. There are no down migrations. To go back, restore the backup made before the upgrade into an empty database. Then start the older binary. The older binary refuses to start on the newer schema. It names the schema version of the database, the schema version of the binary, and the version of the binary.

## What this guide does not cover

These items are not in milestone 3.

- Backup schedules and retention.
- Backups of one namespace.
- GCS and Azure targets.
- A public backup format.
- Encryption of the whole backup.
- Restore of one unit.
- Restore into another region.
