# Backup, restore and upgrade

This guide shows how to back up a whole installation, restore it, and upgrade the server. It follows ADR 30, ADR 33, ADR 38 and ADR 39. The end-to-end suite ran each command in this guide.

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

Check a backup with the check command. It asks the server to check the backup in its configured target. The server reads its configured target. The token comes from `DUSTPAN_ADMIN_TOKEN` or from the file in `DUSTPAN_ADMIN_TOKEN_FILE`. It never comes from a flag.

```sh
opendustpan backup check --server https://dustpan.example --ca-file ca.pem --backup bkp-aaa
```

A complete backup prints `backup bkp-aaa is complete` and exits with status 0. A broken backup exits with status 1 and names the first missing object by key.

## Restore a backup

Restore loads a backup from the configured backup target into the running
installation. Restore is an API operation: the server checks the backup,
backs up the live data, enters maintenance mode, and only then applies the
backup. Use the same key, the same dialect, and the same schema version as
the backup source.

Run the restore command. It calls the server and waits until the restore
leaves the running state. The token comes from `DUSTPAN_ADMIN_TOKEN` or from
the file in `DUSTPAN_ADMIN_TOKEN_FILE`. It never comes from a flag.

```sh
opendustpan restore --server https://dustpan.example --ca-file ca.pem --backup bkp-aaa
```

The command prints the restore ID, the state, and the backup taken before
the restore. It ends with `restore rst-aaa succeeded`. Only one restore runs
at a time. A second start while one runs answers 409.

Before the server applies anything, it checks the manifest, the
key-encryption key, every named blob, and the SQL part, then loads the SQL
data into a staging area and checks the staged data. A failed check stops
the restore and leaves the live data as it was. A check-only request runs
the same checks and applies nothing:

```sh
opendustpan restore --server https://dustpan.example --backup bkp-aaa --check-only
```

Before the apply, the server takes a backup of the live data and names it in
the restore result. To undo the restore, restore that backup. The restore
list names it:

```sh
opendustpan restore --server https://dustpan.example --backup bkp-bbb
```

While the restore applies, the server holds maintenance mode: the tool APIs
answer 503 and the installation API stays available. The server leaves
maintenance mode when the restore ends, also after a failure. The server
does not enter maintenance mode while an update or a run is active, unless
the request sets `--force`, which cancels them first and names them.

The restore increments the failover version of every namespace. Leases,
update tokens, locks and upload capabilities from before the restore stop
working, so no client can write data from before the restore into the
restored data.

The command checks the key before the server writes anything. With no key or
with another key it stops and names `DUSTPAN_KEY_ENCRYPTION_KEY`. A restore
with a wrong key leaves the live data unchanged.

The command refuses a backup of another dialect or another schema version.
The error names both versions. Restore into the same dialect and the same
schema version only. To change version, upgrade the server instead.

The command refuses a backup with no manifest or with an unknown format
number. The manifest is internal in this milestone.

Disaster recovery on a new machine: start the server on an empty database
with the same key-encryption key, then restore through the API. The
bootstrap admin token can restore into the empty installation.

## Upgrade the server

Run the upgrade check before an upgrade. It asks the server for its version and the schema version of its database. It writes nothing. Only the server opens the database. The token comes from `DUSTPAN_ADMIN_TOKEN` or from the file in `DUSTPAN_ADMIN_TOKEN_FILE`. It never comes from a flag.

```sh
opendustpan upgrade check --server https://dustpan.example --ca-file ca.pem
```

The command prints the binary version, the schema version of the database, the schema version that the binary needs, and the names of pending migrations. It ends with one verdict: `up to date`, `upgrade possible`, or `cannot start`. The exit code is 0 for the first two verdicts and 1 for the third and for errors. When migrations are pending, it says to back up the database first and that no downgrade path exists.

Back up the database before each upgrade. `serve` applies pending migrations at start.

For several nodes, start one new node first. The first new node migrates the database. An older node that still runs does not stop. Its next start fails because the schema is newer than the binary knows. Then start the other new nodes.

The project supports no downgrade. There are no down migrations. To go back, restore the backup made before the upgrade through the API. Then start the older binary. The older binary refuses to start on the newer schema. It names the schema version of the database, the schema version of the binary, and the version of the binary.

## What this guide does not cover

These items are not in milestone 3.

- Backup schedules and retention.
- Backups of one namespace.
- GCS and Azure targets.
- A public backup format.
- Encryption of the whole backup.
- Restore of one unit.
- Restore into another region.
