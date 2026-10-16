# Backup, restore and upgrade

This guide shows how to back up a whole installation, check the backup, restore it, and upgrade the server. It follows ADR 30, ADR 33, ADR 38, ADR 39 and ADR 42. The end-to-end suite ran each command in this guide on SQLite and on PostgreSQL.

A backup covers the whole installation. It holds the SQL data with the blob content that the data names. A backup holds no key-encryption key.

The commands are the same for both databases. The server chooses the database: SQLite is the default, and `serve --database postgres://...` selects PostgreSQL. A URL with a password goes in `DUSTPAN_DATABASE` instead, because serve refuses a password in the flag. A backup of one database restores into the other. Section "Restore a backup" gives the rule.

## Back up the installation

Set a backup target on the server. Use the flag or the variable. An empty target means the server takes no backup.

```sh
./bin/opendustpan serve --backup-target dir:/var/backups/dustpan
```

```sh
DUSTPAN_BACKUP_TARGET=dir:/var/backups/dustpan ./bin/opendustpan serve
```

The target holds the layout that the next section describes. The server writes the manifest last. A backup holds sealed OpenTofu state. Protect the target. Use a directory that only the server owner can read.

Start a backup with the backup command. It calls the server and waits until the backup leaves the running state. The token comes from `DUSTPAN_ADMIN_TOKEN` or from the file in `DUSTPAN_ADMIN_TOKEN_FILE`. It never comes from a flag.

```sh
opendustpan backup --server https://dustpan.example --ca-file ca.pem
```

The command takes only `--server` and `--ca-file`. The command prints the backup ID, the state, and the SQL size. It then prints two warnings. Against a local server the output reads, for example:

```text
backup bkp-opx3krqgb4d7iqglf5gq succeeded (SQL 163840 bytes)
A backup cannot decrypt secrets without DUSTPAN_KEY_ENCRYPTION_KEY.
A backup holds sealed OpenTofu state: protect the target.
```

The two warnings name two risks. Without the key-encryption key, the backup keeps secrets unreadable. It holds sealed OpenTofu state, so protect the target. Only one backup runs at a time. A second start while one runs answers 409.

Keep `DUSTPAN_KEY_ENCRYPTION_KEY` apart from the backup target. A backup never holds the key. A lost key still means lost secrets and lost OpenTofu state. Without the key the server cannot open secret values or OpenTofu state after a restore.

The target can also be S3-compatible storage. Set the credentials in `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` and, for temporary credentials, `AWS_SESSION_TOKEN`. Set `--s3-endpoint` for a server other than AWS.

```sh
./bin/opendustpan serve --backup-target s3://dustpan-backups/prod --s3-endpoint https://s3.example
```

An S3-compatible target holds each object with a limit of 5 GiB. Each table file is one object. A table file above 5 GiB fails the backup. The error says that the table file is over the target limit, with its size and the limit. It does not name the table.

## Layout of a backup

A backup is a set of objects under the target:

- `backups/<id>/manifest.json` describes the backup. It is the last object that the server writes. A backup without it is not complete.
- `backups/<id>/sql/<table>.jsonl.gz` holds the SQL data. There is one file for each table of the installation. Each file is gzip-compressed JSON Lines: one row for each line, with one member for each column.
- `blobs/<key>` holds each blob that the SQL data names, such as an OpenTofu state version or a Pulumi checkpoint.

The manifest records the backup format (`format`: 2) and the SQL format (`sqlFormat`: `portable`). It records the database that wrote the backup (`dialect`) and the schema version. It also records the home regions and the wrapper IDs.

For each table file, the manifest has one entry in `sqlFiles`. The entry names the file, its columns, its row count, its size and its SHA-256. The manifest also records each blob with its key, size and MD5.

The table files use one encoding for both databases. A backup of SQLite restores into PostgreSQL, and a backup of PostgreSQL restores into SQLite.

## Check a backup

Check a backup with the check command. It asks the server to check the backup in its configured target. The check reads the manifest, every table file and every blob, and compares each object with the size and checksum that the manifest names. The token comes from `DUSTPAN_ADMIN_TOKEN` or from the file in `DUSTPAN_ADMIN_TOKEN_FILE`. It never comes from a flag.

```sh
opendustpan backup check --server https://dustpan.example --ca-file ca.pem --backup bkp-aaa
```

The command takes only `--server`, `--ca-file` and `--backup`. A complete backup prints `backup bkp-aaa is complete` and exits with status 0. A broken backup exits with status 1 and names the first missing object by key.

## Restore a backup

Restore loads a backup from the configured backup target into the running
installation. Restore is an API operation: the server checks the backup,
backs up the live data, enters maintenance mode, and only then applies the
backup. Use the same key-encryption key and the same schema version as the backup
source. The database may differ: a backup of SQLite restores into PostgreSQL
and a backup of PostgreSQL restores into SQLite (ADR 42, decision 9). A backup
from before the seal change still restores.
After the apply, the server seals the rows that the backup holds unsealed.

Run the restore command. It calls the server and waits until the restore
leaves the running state. The token comes from `DUSTPAN_ADMIN_TOKEN` or from
the file in `DUSTPAN_ADMIN_TOKEN_FILE`. It never comes from a flag.

```sh
opendustpan restore --server https://dustpan.example --ca-file ca.pem --backup bkp-aaa
```

The command takes only `--server`, `--ca-file`, `--backup`, `--check-only` and `--force`. It takes no `--from`, `--database`, `--blob-dir` or `--blob-store`: the server restores from its own `--backup-target` into its own stores. The command prints the restore ID, the state, and the backup taken before
the restore. Against a local server the output reads, for example:

```text
restore rst-qvxfikelldws37gdzxwg succeeded (SQL 163840 bytes)
backup before restore: bkp-cnsz7imkyfh4mcrk5n3z
```

It ends with `restore rst-aaa succeeded`. Only one restore runs
at a time. A second start while one runs answers 409.

Before the server applies anything, it checks the manifest, the
key-encryption key, every named blob, and the SQL part, then loads the SQL
data into a staging area and checks the staged data. A failed check stops
the restore and leaves the live data as it was. A check-only request runs
the same checks and applies nothing:

```sh
opendustpan restore --server https://dustpan.example --backup bkp-aaa --check-only
```

A check-only restore prints that it changed nothing:

```text
restore rst-kxhi2mzykvpfartzahi4 succeeded (SQL 163840 bytes)
check only: no data changed
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

The command refuses a backup of another schema version. The error names both
versions. Restore into the same schema version only. To change version, upgrade
the server instead.

Before the server changes anything, it checks the manifest, the
key-encryption key, each table file against its descriptor, and each blob
against its size and MD5. It loads the table files into a staging area. The
stage checks the staged data against the row counts, the blob references and
the foreign keys of the manifest. A failed check leaves the live data unchanged.

The command refuses a backup with no manifest or with an unknown format
number. This server also refuses a backup in the native format of the older
server (manifest format 1), and the message says to take a new backup.
This server does not restore that format. To keep such a backup, follow the
steps in "Move a format 1 backup". The refusal does not change the live data.

Disaster recovery on a new machine: start the server on an empty database
with the same key-encryption key, then restore through the API. The
bootstrap admin token can restore into the empty installation. The new
database may be the other kind.

A restore replaces the access tables only when the caller holds server:restore-access, which only the site administrator holds. Any other restore keeps the current principals, roles, grants, tokens and sign-in providers. A revoked token stays revoked, and a disabled sign-in provider stays disabled. The sign-in providers include their client secrets and the issuer of each provider ID.

A restore needs the key-encryption key that sealed every key in the backup, also when it keeps the access tables. The check opens a key of the backup with it: a key of a namespace, or else a key of the sign-in providers. A restore with another key stops before it changes anything.

## Move a format 1 backup

A format 1 backup holds the native format of the older server. An older server
writes format 1 again, so a new backup from the older server does not help.
The data moves through a restore into the older server, then an upgrade, then
a new backup. Use these steps:

1. Keep the format 1 backup in its target. Do not delete it.
2. Start the older server version that wrote the backup. Use an empty
   database and the same key-encryption key. Set `--backup-target` to the
   target that holds the format 1 backup.
3. Restore the format 1 backup into that server:

   ```sh
   opendustpan restore --server https://old.example --ca-file ca.pem --backup bkp-aaa
   ```

4. Check that the restore succeeded and that the data is present.
5. Stop the older server. Run the upgrade check for this server version, then
   start this server version on the same database. The start applies the
   pending migrations.
6. Take a new backup with this server version. The new backup is format 2:

   ```sh
   opendustpan backup --server https://dustpan.example --ca-file ca.pem
   ```

7. Check the new backup. Keep the format 1 backup until the format 2 backup
   checks as complete.

## Upgrade the server

Run the upgrade check before an upgrade. It asks the server for its version and the schema version of its database. It writes nothing. Only the server opens the database. The token comes from `DUSTPAN_ADMIN_TOKEN` or from the file in `DUSTPAN_ADMIN_TOKEN_FILE`. It never comes from a flag.

```sh
opendustpan upgrade check --server https://dustpan.example --ca-file ca.pem
```

The command takes only `--server` and `--ca-file`. Against a local server with no pending migration the output reads, for example:

```text
binary version: dev
server version: dev
database schema version: 19
binary schema version: 19
schema minimum binary version: none
pending migrations: none
verdict: up to date
```

The command prints the binary version, the schema version of the database and the schema version that the binary needs. It then prints the minimum binary version of the schema and the names of pending migrations. It ends with one verdict: `up to date`, `upgrade possible`, or `cannot start`. The verdict is `cannot start` only when the binary version is below the minimum binary version of the schema. A schema can record migrations that the binary does not know. The verdict is then `up to date` when the binary is at or above the minimum: the binary starts on it and applies nothing. The exit code is 0 for the first two verdicts and 1 for the third and for errors. When migrations are pending, it says to back up the database first and that no downgrade path exists. When the server runs without a key-encryption key, or is too old to report one, the command also prints a warning. The warning names `--generate-key-encryption-key` (`DUSTPAN_GENERATE_KEY_ENCRYPTION_KEY=true`), `--key-encryption-key-file` (`DUSTPAN_KEY_ENCRYPTION_KEY_FILE`) and `DUSTPAN_KEY_ENCRYPTION_KEY`. Set one of them before the upgrade.

Back up the database before each upgrade. `serve` applies pending migrations at start.

### Minimum binary version

The schema records the oldest binary version that can use it: the minimum binary version. A migration that deletes or narrows a table or a column raises the minimum. A schema from before the minimum column records no minimum. A binary starts on a schema when its version is at or above the minimum, also when the schema is newer than the binary. It then applies no migration. A binary below the minimum does not start. The error names the binary version and the minimum, and it changes nothing in the database.

The server compares only `vMAJOR.MINOR.PATCH`. A version that `git describe` makes, for example `v0.2.0-3-gabc1234-dirty`, counts as its tag `v0.2.0`. A build with no release version, for example `dev` or a commit hash, counts as newer than every release. No minimum stops it. When such a build starts on a schema with a minimum, it logs the warning `dev build ignores the minimum binary version of the database schema`. The upgrade check prints a warning when the command or the server is such a build.

For several nodes, start one new node first. The first new node migrates the database. An older node that still runs does not stop. Its next start fails only when the new schema raised the minimum above its version. Then start the other new nodes.

The project supports no downgrade. There are no down migrations. To go back, restore the backup made before the upgrade through the API. Then start the older binary. When the older binary is below the minimum binary version of the schema, it refuses to start. The error names the binary version and the minimum.

## Known limit

Restore reads each backup file through gzip with no size bound. The server
does not stop a file that expands far beyond its size. Keep backups in a
store that only the operator can write. The files then come from the
operator's own store, not from a source that others can change.

## What this guide does not cover

These items are not in milestone 3.

- Backup schedules and retention.
- Backups of one namespace.
- GCS and Azure targets.
- A public backup format.
- Encryption of the whole backup.
- Restore of one unit.
- Restore into another region.
