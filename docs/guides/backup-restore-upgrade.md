# Backup, restore and upgrade

This guide shows how to back up a whole installation, check the backup, restore it, and upgrade the server. The end-to-end suite ran each command in this guide on SQLite and on PostgreSQL.

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

The target holds the layout that the next section describes. The server writes the manifest last. A backup holds OpenTofu state that the server encrypted. Protect the target. Use a directory that only the server owner can read.

Start a backup with the backup command. It calls the server and waits until the backup leaves the running state. The token comes from `DUSTPAN_ADMIN_TOKEN` or from the file in `DUSTPAN_ADMIN_TOKEN_FILE`. It never comes from a flag.

```sh
opendustpan backup --server https://dustpan.example --ca-file ca.pem
```

The command takes only `--server` and `--ca-file`. The command prints the backup ID, the state, and the SQL size. It then prints two warnings. Against a local server the output reads, for example:

```text
backup bkp-opx3krqgb4d7iqglf5gq succeeded (SQL 163840 bytes)
A backup cannot decrypt secrets without DUSTPAN_KEY_ENCRYPTION_KEY.
A backup holds OpenTofu state that the server encrypted: protect the target.
```

The two warnings name two risks. Without the key-encryption key, the backup keeps secrets unreadable. It holds OpenTofu state that the server encrypted, so protect the target. Only one backup runs at a time. A second start while one runs answers 409.

A backup keeps running when you start or restart a server process, or when you update the server processes one at a time. If the server process that runs the backup stops, another server process continues the backup after the lease ends. The lease is 30 seconds by default. The backup writes each object to the same key again, so the second run replaces the objects of the first run. Any server process answers the state of the backup. A backup that fails ends as `failed` with the error text.

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
- `blobs/<key>` holds each blob that the SQL data names. Examples are OpenTofu state versions and Pulumi checkpoints. Other examples are job plan parts, job log chunks and registry release files.

The manifest records the backup format (`format`: 3) and the SQL format (`sqlFormat`: `portable`). It records the database that wrote the backup (`dialect`) and the schema version. It also records the primary installations and the wrapper IDs. It records the kind of the installation that wrote the backup (`installationKind`: `dev` or `production`). The server reads the kind from its own database. A backup of another format number is not valid, and neither is a manifest with no kind or with an unknown kind.

For each table file, the manifest has one entry in `sqlFiles`. The entry names the file, its columns, its row count, its size and its SHA-256. The manifest also records each blob with its key, size and MD5. The backup copies each file of a release with its size and MD5.

The publish path limits each file of an upload to 512 MiB and the sum of the files of one release to 2 GiB. These values are fixed in the server.

The server copies a blob only when the SQL data holds its size and its MD5. It checks the copy against these values. A row can name a blob without them, for example a row that an earlier version of the server wrote. The server does not copy that blob. It lists the key in the optional `unverified` list of the manifest and writes a warning to the server log.

A restore accepts a backup only when the `unverified` list names every blob that the SQL data names without these values. A backup that does not list such a blob lost its content. The restore then stops with the text `backup is incomplete: the data names <number> blobs that the backup does not hold`. The live data stays as it was.

The server refuses a backup when its data names more than 10000 such blobs. It writes no manifest, so no restore applies the backup. The failed row and the server log name the count and the limit. The count points to rows without the size and the MD5 of a blob. Such rows can come from an earlier version of the server. Run the backup again after those rows hold their values, or after the server no longer holds them. No setting changes the bound. The answer of `GET /api/server/backups/{id}` shows it as `unverifiedKeyLimit`. The same answer shows the count of the unverified keys of the manifest as `unverifiedKeyCount`. The code `backup.too-many-unverified` carries the count and the limit as its arguments.

A restore does not apply the bound. A manifest with more unverified keys than the bound restores as before. Such a manifest can come from an earlier version of the server. The restore checks the same rule: the list must name every blob that the data names. The audit log then holds one entry with the count and the limit.

The table files use one encoding for both databases. A backup of SQLite restores into PostgreSQL, and a backup of PostgreSQL restores into SQLite.

## Check a backup

Check a backup with the check command. It asks the server to check the backup in its configured target. The check reads the manifest, every table file and every blob, and compares each object with the size and checksum that the manifest names. It also compares the totals of the manifest with its `sqlFiles` entries. `sqlSize` must equal the sum of the file sizes, and `sqlSha256` must equal the SHA-256 of the entries. Each value of `rowCounts` must equal the row count of the entry of its table. The token comes from `DUSTPAN_ADMIN_TOKEN` or from the file in `DUSTPAN_ADMIN_TOKEN_FILE`. It never comes from a flag.

```sh
opendustpan backup check --server https://dustpan.example --ca-file ca.pem --backup bkp-aaa
```

The command takes only `--server`, `--ca-file` and `--backup`. A complete backup prints `backup bkp-aaa is complete` and exits with status 0. A broken backup exits with status 1 and names the first missing object by key, or the total that does not match.

## Restore a backup

Restore loads a backup from the configured backup target into the running
installation. Restore is an API operation: the server checks the backup,
backs up the live data, enters maintenance mode, and only then applies the
backup. Use the same key-encryption key and the same schema version as the backup
source. The database may differ: a backup of SQLite restores into PostgreSQL
and a backup of PostgreSQL restores into SQLite.

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

The server answers the start with 202 and the restore ID. A `worker` process
runs the restore in steps, and records the result of each step in the
database. Run at least one `worker` process. When no worker claimed work for
2 lease periods, the start answers 503 with the error code `worker.absent`. When the
process that runs a restore stops, another worker continues the restore after
the lease ends. The [several instances guide](several-instances.md) gives the
steps.

The worker that runs a restore renews the claim of the restore while it works. During the swap on PostgreSQL, a renewal waits from the copy of the durable control tables to the end of the swap. That time grows with the size of the installation. On SQLite, a renewal waits for the whole swap. A renewal can fail, for example when the database does not answer. Then the worker stops the step and gives the claim back before the lease ends. The worker log then has a WARN entry `durable operation claim renewal failed`, with the operation ID, the kind `restore`, the attempt and the owner. When another worker took the claim, the entry is `durable operation claim lost; another instance can own the operation`. The restore keeps its state, for example `applying`, and `GET /api/server/restores/{id}` shows it. A worker then claims the restore again and runs the stopped step again from its start. The swap is one database transaction, so a stopped swap changes no data. Do not start the restore again.

Before the restore changes data, it takes a backup of the live data, the backup before restore. The worker that runs the restore also runs this backup, under a claim that the start of the backup takes. Another worker continues this backup only when that claim ends or expires. This backup checks each live blob against the size and MD5 in its own live row. When a live blob is absent, or its bytes have another size or MD5 than its row, the backup does not stop. It records the blob in the `damaged` list of its manifest. Each entry has the key, the state (`missing` or `damaged`) and the reason (`absent`, `size`, `checksum` or `sealed`). It also has the size and MD5 of the live row. The backup holds no copy of these blobs. Sealed OpenTofu state that does not open goes in the list too, with the reason `unopened`. Damaged bytes cause it, and so does a wrong key-encryption key. Every other read error stops the backup and the restore. When the backup finds missing or damaged blobs, the audit log holds one entry with the counts and the first keys.

Blobs are write-once. A restore adds keys and never writes over a live blob. So the restore stops before it changes any live data in these cases:

- The backup before restore found a live blob whose bytes do not match its record or do not open. The text names the count and the first keys. It says that the restore changed no live data. Check also that the server runs with the key-encryption key that wrote the data. Put the good bytes back at these keys from a copy of the blob store. A snapshot of the volume or of the bucket is such a copy. Then run `opendustpan restore` again.
- The backup before restore found more live blobs missing than the bound allows. The bound is more than 10 blobs. It is also more than 20 percent of the blobs that the live data names, when more than 1 blob is missing. So one missing blob never stops a restore. A store with every blob missing stops it when the live data names more than 1 blob. When the live data names 1 blob and it is missing, the restore goes on and writes it. Many missing blobs point to a blob store setting that names another directory, bucket or prefix, not to lost blobs. The text names the count and the bound. Check the blob store setting of the server with `opendustpan store check --target blobs`, then run `opendustpan restore` again.

`opendustpan restore` prints this text and the next step. It does not tell you to run the same restore again before you put the good bytes back or change the blob store setting.

When the command cannot read a restore that started, for example while the database of the server is not available, it names the restore ID. The server resumes the restore on its own, so you do not run it again. Read its state with `GET /api/server/restores/{id}`.

Each of these stops has an error code in the restore answer (`errorCode`): `restore.live-blobs-damaged` or `restore.live-blobs-missing`. `opendustpan restore` chooses its next step from the code.

A restore always takes a new backup before restore. A pre-backup ID can name a backup that exists, or a backup that another restore started. Then the restore stops before any live change with the code `restore.pre-backup-foreign`. Run the restore again.

No setting changes the bound. The answers of `POST /api/server/restores` and `GET /api/server/restores/{id}` show it as `missingBlobLimit`, with `count` (10), `percent` (20) and `percentAbove` (1).

For each blob of the backup, the restore does one of these:

- The key is absent from the blob store of the server: the restore writes the blob.
- The key holds the bytes of the manifest: the blob stays as it is.
- The key holds other bytes: the restore stops and writes nothing over them. These bytes matched their live row, so they are live data that is newer or different. The restore then fails with the text `backup data is corrupt`, and the audit log holds an entry that names the key. `--force` does not change this.

A restore that went on after the backup before restore found missing blobs succeeds with the warning `missing-live-blobs`. The answer of `GET /api/server/restores/{id}` has `preBackupFound`. It holds the count of blobs that the live data names (`named`), the counts `missing` and `damaged`, and the first 10 keys (`keys`). A restore that stopped at the live blobs has `preBackupFound` too.

A backup before restore that has a `damaged` list stays restorable, so that it can undo the restore. Only a backup that a restore of this installation started can have this list. `opendustpan backup check` and a restore refuse any other backup with a `damaged` list as corrupt. `opendustpan backup check` checks the objects that it holds. The backup holds no copy of the blobs of the `damaged` list. So a restore of it writes nothing to those keys and leaves them as they are. That restore succeeds with the warning `blobs-not-held`, and its answer has `notHeld` with the counts and the first keys.

The restore does not rely on the conditional writes of the blob store. It reads the key again just before it writes. It also reads the staged copy of the blob and checks it against the manifest before it writes the live key. A staged copy that differs stops the restore, and the live key stays absent. Before each write of a blob, the restore checks that its worker still holds the claim on the restore.

A blob store that ignores conditional writes can still take a write of another writer between the last read and the write of the restore. Maintenance mode stops every other writer while the restore copies blobs. This is the protection against that gap.

During a rolling deploy, a worker that runs a binary below the minimum binary
version of the schema cannot claim a restore operation and never runs its
steps. The operation stays pending until a worker on a new binary takes it.

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

While the restore applies, the server holds maintenance mode. The tool APIs
answer 503. A write to the installation API also answers 503 with
`Retry-After`: the restore replaces tokens, service tokens, principals,
grants, groups, roles, namespaces and policies, and a write in this time
would be lost. Reads of the installation API, the maintenance routes and the
backup and restore routes stay available. The server leaves maintenance mode
when the restore ends, also after a failure or a cancel. A maintenance mode
that an operator entered before the restore stays. The server does not enter
maintenance mode while an update or a run is active, unless the request sets
`--force`, which cancels them first and names them. If work starts during a
forced enter, the server tries again and cancels that work too, up to three
attempts. If work keeps starting, the server answers 409 and stays in normal
mode. The answer names the work that blocked the enter and, in `canceled`, the
work that the attempts canceled. Run the command again to cancel the work
that started last.

Each write checks the stored installation state in its own database
transaction. After one server enters maintenance mode, every other server
refuses writes in its next request. A write that is already in progress
ends before the enter returns. A server that cannot read the installation
state refuses the write.

The enter waits for the writes in progress to end: up to 30 seconds on SQLite
and up to 10 seconds on PostgreSQL. If they do not end in that time, the enter answers 503 with
`Retry-After: 30` and the message `cannot enter maintenance mode: the writes
in flight did not end in time: try again later`, and the installation stays
in normal mode. Run the command again.

The audit log keeps its entries in maintenance mode: an entry that records a
refusal, a sign-in or a restore is never dropped. A console session that
exists stays valid, but its last-use time does not change. A new sign-in is
not possible until the server leaves maintenance mode.

The restore increments the failover version of every namespace. Leases,
update tokens, locks and upload capabilities from before the restore stop
working, so no client can write data from before the restore into the
restored data. The restore also releases every OpenTofu workspace lock that
the backup holds. A client that holds a lock from before the restore must take
a new lock and read the restored state before it writes.

The server checks the key that it started with before it writes anything. The
`opendustpan restore` command does not read `DUSTPAN_KEY_ENCRYPTION_KEY`. When
the server has no key, or its key does not open the backup, the restore stops,
names the key settings of the server, and leaves the live data unchanged. To
restore a backup on a new server, start that server with the key of the server
that made the backup (`DUSTPAN_KEY_ENCRYPTION_KEY` or
`DUSTPAN_KEY_ENCRYPTION_KEY_FILE`), then run the restore.

The command refuses a backup of another schema version. The error names both
versions. Restore into the same schema version only. To change version, upgrade
the server instead.

The command also refuses a backup with a schema version below 168, the
baseline schema version. The baseline migration replaced every migration
before it, so no binary can restore such a backup. The error names the
baseline.

Before the server changes anything, it checks the manifest, the
key-encryption key, each table file against its descriptor, and each blob
against its size and MD5. It loads the table files into a staging area. The
stage checks the staged data against the row counts, the blob references and
the foreign keys of the manifest. A failed check leaves the live data unchanged.

The command refuses a backup with no manifest or with an unknown format
number. The refusal does not change the live data.

Disaster recovery on a new machine: start the server on an empty database
with the same key-encryption key, then restore through the API. The
bootstrap admin token can restore into the empty installation. The new
database may be the other kind.

A restore replaces the access tables only when the caller holds server:restore-access, which only the site administrator holds. Any other restore keeps the current principals, roles, grants, tokens, preferences of people and sign-in providers. It also keeps the registration of the GitHub App and the map of GitHub installations to namespaces. A revoked token stays revoked, and a disabled sign-in provider stays disabled. The sign-in providers include their client secrets and the issuer of each provider ID.

A restore needs the key-encryption key that encrypted every key in the backup, also when it keeps the access tables. The check opens a key of the backup with it: a key of a namespace, or else a key of the sign-in providers. A restore with another key stops before it changes anything.

### Cancel a restore

A restore can stop before it swaps the staged data into the live data. No CLI
command cancels a restore. Send the request to the installation API with a
token that has the action `server:restore`:

```sh
curl -X POST -H "Authorization: Bearer $DUSTPAN_ADMIN_TOKEN" \
  https://dustpan.example/api/server/restores/rst-aaa/cancel
```

| Answer | Meaning |
| --- | --- |
| 202 | The restore stops before the swap. Its row ends failed with the text `restore canceled before the swap`. A second cancel answers 202 again. The restore row can stay `checking` until the step that runs ends. In one test run this took about 6 seconds. Read `GET /api/server/restores/{id}` until its status is `failed`. |
| 409 `restore.swapped` | The restore already swapped the data. It runs to its end. To undo it, restore the backup taken before it. |
| 409 `restore.ended` | The restore already ended. |
| 404 | No restore has this ID. |

Each cancel of a known restore writes an audit entry. A canceled restore
leaves the live data unchanged, and leaves the maintenance mode that it
entered. The audit log of a restore that stopped before the swap holds an
entry with the outcome `failure` and the text `canceled before the swap` (or
`failed before the swap`, or `stopped before the swap` for a restore that
ended with no end write). The text says that the live state, history and
access tables did not change. The entry that the
server writes when it accepts the start says `requested`, with the outcome
`success`, because it records the accepted request only. A restore that fails
after the swap writes an entry with the outcome `failure` and the text
`failed after the swap`. The server increments the failover version of every namespace after
a restore that stopped with no end, also after a cancel.

### Staging areas and temporary blobs

A restore loads the backup into a staging area. On PostgreSQL the staging area
is a schema named `restore_stage_<id>_<n>`, and after the swap the old live
data stays in a schema named `restore_prev_<id>` until the restore drops it.
On SQLite the staging area is a file next to the database file, named
`<database>.stage-rst-<id>-<n>`. A blob that the restore copies goes first to
a temporary key under `restore-tmp/` in the live blob store.

The last steps of a restore drop its staging areas and delete its temporary
keys. A restore that fails drops them too. A restore that stopped with no end
can leave them: the next restore drops them. A restore that you cancel counts
as a restore that stopped with no end. A cancel in the check step can leave the
PostgreSQL schemas `restore_stage_<id>_<n>` and `restore_stage_<id>_<n>_control`
until the next restore drops them. If you take your own dump of a
PostgreSQL database, leave out these schemas:

```sh
pg_dump --exclude-schema='restore_*' "$DATABASE_URL"
```

The restore never deletes a live blob and never writes over one. When a live
blob is damaged, or has other bytes that differ from the backup, the restore
stops and writes nothing over it. See [Restore a backup](#restore-a-backup).

### Blob keys that a restore refuses

A restore writes only blob keys of the form `namespaces/<namespace ID>/...`.
The manifest of a backup is not signed, so a person who can write to the
backup target could name another key. The restore refuses a manifest that names
a key under `audit-checkpoints/`, `store-checks/` or `restore-tmp/`, and any
other key outside `namespaces/<namespace ID>/`. It makes these checks before it
stages or writes anything. The restore ends failed with the text "backup names
a blob key that a restore does not write", and writes an audit entry that names
the key. To find the cause, compare the manifest of the backup with a copy that
you trust.

The backup itself makes the same check. When a row of the data names a blob
under such a prefix, the backup fails with a text that names the key and tells
you to delete the row that names it. No backup is written that a restore would
refuse. A restore also pins the whole manifest when it stages the backup, and it
refuses a manifest that changed in the target after that.

### Revocations in maintenance mode

A write that only reduces access runs in maintenance mode. You can end a
compromised session or revoke a compromised token at any time, also while a
restore applies. These writes run, each with its audit entry:

- a sign-out, which ends the session of the person.
- the end of a session by an administrator.
- the revocation of an access token or a service token.
- the revocation of a grant. A grant only allows, and a limit is a ceiling, so
  the end of a grant never gives more access. The checks of a
  revocation in normal operation stay in force: the fence of the namespace,
  and the rule that the caller holds every action that the grant gives.
- the revocation of a join token of a runner pool
  (`POST /api/runners/{namespace}/pools/{pool}/join-tokens/{id}/revoke`).
- the retire of a runner
  (`POST /api/runners/{namespace}/runners/{runner}/retire`), which also ends
  the live claims of the runner. The other routes of the runner management API
  answer 503 in maintenance mode.
- the disable of a sign-in provider, which also ends its sessions. A change of
  the provider that sets one more field, and the enable of the provider, stay
  refused.
- the delete of a sign-in provider that no principal uses
  (`DELETE /api/server/sign-in-providers/{id}`). A provider that a principal
  uses answers 409. A restore of older data does not bring the deleted
  provider back as an active provider: the restore disables it.
- the disable of a principal (`POST /api/server/principals/{id}/disable`). The
  grants and tokens of the principal stay, and the principal can not use them
  until an administrator enables it again. The check of a disable in normal
  operation stays in force: the caller holds every action that the grants of
  the principal give. The server refuses the disable of the last enabled
  break-glass account with 409 and the error code
  `principal.no-working-break-glass`, also in maintenance mode. Keep a second enabled break-glass account first
  (see [Keep a break-glass account](break-glass-accounts.md)).
- the expiry of a temporary administrator, which ends the record, disables the
  principal, revokes its grants and tokens and ends its group memberships.

A write that grants or widens access stays refused with 503. This includes a
new sign-in, a new session, a new token, a new grant and the enable of a
disabled principal. These writes stay refused too, because they can give more
access in some cases: the removal of a member of a group (a rule that forbids
the group could stop to match), the delete of a principal, a role or a group,
and the inactivity sweep.

A restore replaces the sessions, tokens, grants and principals with the data
of the backup, so it would bring back a session or a token that you revoked in
maintenance mode. The server prevents this. It records each revocation that
it accepts in maintenance mode, and the restore applies the record again
after it swaps in the backup. The restore ends the restored session, revokes
the restored token, grant or join token, retires the restored runner, disables the restored principal or
provider, and repeats the expiry of the restored temporary administrator, with
the time of the original revocation. A target that the backup does not hold
needs no change.

The audit log of the backup replaces the audit log of the live data, so the
entries of the revocations in the stay are not in the log after the restore.
For each revocation that it applies again, the restore writes one entry in
`@server` with the action `maintenance:replay-revocation`. The entry names the
subject and the object of the original revocation and has the time of the
original revocation. A record in the journal that the restore can not apply (a
kind that the server does not know, or a target that is not an ID) changes
nothing and gets an entry with the outcome `denied`. The restore skips a
disable that would leave the restored data with no enabled break-glass
account. It keeps that account enabled and writes an entry with the outcome
`denied` and the reason `principal.no-working-break-glass` (see
[Keep a break-glass account](break-glass-accounts.md)). The restore stops
with the code `principal.no-working-break-glass` when it lowers the count of
working break-glass accounts and leaves fewer than two
(see [Other paths](break-glass-accounts.md#other-paths)).

The server records only the revocations of the current stay in maintenance mode. A
new enter starts a new stay. A revocation that you made in normal
operation, or in an earlier stay, belongs to the data that the restore
replaces. After a restore, check the sessions and tokens that you revoked
before the stay, and revoke them again if the backup brings them back. A
restore by a caller without `server:restore-access` keeps the live sessions,
tokens, grants and principals, so it keeps every revocation.

## Restore a backup of a dev installation

A backup of an installation that `opendustpan dev` made has the kind `dev`. It holds the person `dev:admin`, with the role of the site administrator, and maybe its sessions and tokens. A dev backup in a production installation brings these into it.

Three layers stop that:

- A production installation refuses to restore a backup of the kind `dev`. The restore answers status 409, before it enters maintenance mode, and it changes nothing:

  ```text
  This installation is a production installation. The backup is from a dev installation. We do not recommend this restore. To restore it, start the server with `--unsafe-allow-restoring-dev-installation-backups-into-production`.
  ```

  A check-only restore gets the same refusal. A dev installation restores a dev backup as before. A dev installation also restores a production backup. The answer then carries the warning `production-backup-into-dev`: the tokens and sessions of the backup are now in a dev data directory.
- A restore into a production installation deletes the dev data from the staged data before it swaps, whatever the manifest says. The manifest is not signed, so the staged data decides. The restore deletes the people with the subject prefix `dev:` in any letter case, and their grants, group memberships, sessions, tokens and open sign-in requests. It deletes the sign-in provider with the ID `dev`. It deletes the dev subjects from the approvers of approval rules, approval defaults and ownership rules. A backup that holds such data and is not allowed is refused, and the live data stays as it was.
- A production server refuses a session or a token of a person with the prefix `dev:`.

To restore a dev backup into a production installation, start the server with the flag `--unsafe-allow-restoring-dev-installation-backups-into-production`. The flag has no environment variable and no configuration file key, and no API sets it. The server writes one warning to the log at each start with the flag. The restore then goes on and deletes the dev data. The answer of the restore carries the warning `dev-backup-into-production`:

```text
This restore deleted the dev users and their grants. Things that depend on them may break. We do not recommend this restore.
```

A check-only restore carries the warning `dev-backup-would-remove`. Its text says that the restore would delete the dev users, and that the check changed no data.

An approval rule can lose its last approver. The restore then writes the value `!no-approver` as its approver. Nobody can meet that rule, and the gate refuses the change that it covers until a person changes the rule.

`opendustpan restore` reads the manifest first. It prints a notice before it starts the restore of a dev backup. The answer of a restore lists the warnings, and the answer of a check-only restore also shows the dev data that the restore would delete:

```text
restore rst-aaa succeeded
warning: This restore deleted the dev users and their grants. Things that depend on them may break. We do not recommend this restore.
dev data: 1 principals; grants 1, principals 1, sessions 1
```

The restore stores a report of the deletion in its record. It writes one audit entry for each deleted principal, up to 100, and one entry for the other principals. It writes one entry for each class of data that no principal owns. If an audit entry cannot be written, the restore still ends as finished, and the answer carries the warning `audit-append-failed`. The audit log keeps its entries: the restore deletes no audit entry.

## Upgrade the server

Run the upgrade check before an upgrade. It asks the server for its version and the schema version of its database. It writes nothing. Only the server opens the database. The token comes from `DUSTPAN_ADMIN_TOKEN` or from the file in `DUSTPAN_ADMIN_TOKEN_FILE`. It never comes from a flag.

```sh
opendustpan upgrade check --server https://dustpan.example --ca-file ca.pem
```

The command takes only `--server` and `--ca-file`. Against a local server with no pending migration the output reads, for example:

```text
binary version: dev
server version: dev
database schema version: 100
binary schema version: 100
schema minimum binary version: none
pending migrations: none
running journaled updates: 0
warning: this binary is a dev build. It ignores the minimum binary version of the database schema.
warning: the server runs a dev build. It ignores the minimum binary version of the database schema.
verdict: up to date
```

The command prints the binary version, the schema version of the database and the schema version that the binary needs. It then prints the minimum binary version of the schema and the names of pending migrations. It ends with one verdict: `up to date`, `upgrade possible`, or `cannot start`. The verdict is `cannot start` only when the binary version is below the minimum binary version of the schema. A schema can record migrations that the binary does not know. The verdict is then `up to date` when the binary is at or above the minimum: the binary starts on it and applies nothing. The exit code is 0 for the first two verdicts and 1 for the third and for errors. When migrations are pending, it says to back up the database first and that no downgrade path exists. When the server runs without a key-encryption key, or is too old to report one, the command also prints a warning. The warning names `--key-encryption-key-file` (`DUSTPAN_KEY_ENCRYPTION_KEY_FILE`), `DUSTPAN_KEY_ENCRYPTION_KEY` and the command `opendustpan key generate --out <path>`. Set a key before the upgrade.

Back up the database before each upgrade. `serve` applies pending migrations at start.

`serve` refuses a database that records a schema version below 168, the baseline schema version. The baseline migration replaced every migration before it, so no binary can upgrade such a database. The error names both versions. Start the server on an empty database. The upgrade check gives the verdict `cannot start` for such a database and names the baseline.

The database records the SHA-256 of each migration. Until the first tag, a change to the schema edits the baseline in place. A build with another version of the baseline refuses the database at start, and names both values. `serve` says to restore a backup of the same build, or to set `--database` to a new database. It never offers a reset. An untagged build has no upgrade path. The upgrade check cannot see an edit of the baseline, because the name of the file does not change. For a dev data directory, run `opendustpan dev reset`, as the [dev server guide](dev-server.md#start-again-after-a-schema-change) shows.

### Minimum binary version

The schema records the oldest binary version that can use it: the minimum binary version. A migration that deletes or narrows a table or a column raises the minimum. A schema from before the minimum column records no minimum. A binary starts on a schema when its version is at or above the minimum, also when the schema is newer than the binary. It then applies no migration. A binary below the minimum does not start. The error names the binary version and the minimum, and it changes nothing in the database.

A binary version is `MAJOR.MINOR.PATCH`, with or without a `v`. A version that `git describe` makes, for example `v0.2.0-3-gabc1234-dirty`, counts as its tag `v0.2.0`. A pre-release, for example `v0.2.0-rc.1`, is below its release `v0.2.0`. A build with no release version (`dev`, an empty version or a commit hash) counts as newer than every release. No minimum stops it. The server refuses to start with any other version, so a wrong version stamp cannot skip the minimum. A binary also refuses to apply a migration whose minimum is above its own version.

A recorded minimum is always `vMAJOR.MINOR.PATCH`. Older binaries refuse a minimum in any other form, so this form does not change. When such a build starts on a schema with a minimum, it logs the warning `dev build ignores the minimum binary version of the database schema`. The upgrade check prints a warning when the command or the server is such a build.

For several nodes, start one new node first. The first new node migrates the database. An older node that still runs does not stop. Its next start fails only when the new schema raised the minimum above its version. Then start the other new nodes.

The project supports no downgrade. There are no down migrations. To go back, restore the backup made before the upgrade through the API. Then start the older binary. When the older binary is below the minimum binary version of the schema, it refuses to start. The error names the binary version and the minimum.

## Release checklist

Before a tag goes out, copy the migration set of the tag into the test
data. The schema upgrade test then covers upgrades from that tag without
git. Run for the tag, for example `v0.1.0`:

```sh
./scripts/copy-tag-migrations.sh v0.1.0
```

The script copies `pkg/store/sqlite/migrations/*.sql` and
`pkg/store/postgres/migrations/*.sql` of the tag, with their `.sql.sha256`
hash files, into `testdata/tag-migrations/<tag>/` of each store adapter. The
frozen test checks each copy against its hash file, so no hash is recorded by
hand. Commit the copies in the same change. Then run the schema upgrade tests
and `make upgrade-test`.

## Known limit

Restore reads each backup file through gzip with no size bound. The server
does not stop a file that expands far beyond its size. Keep backups in a
store that only the operator can write. The files then come from the
operator's own store, not from a source that others can change.

## What this guide does not cover

These items are not in this release.

- Backup schedules and retention.
- Backups of one namespace.
- GCS and Azure targets.
- A public backup format.
- Encryption of the whole backup.
- Restore of one unit.
- Restore into another installation.
