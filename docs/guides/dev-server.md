# Run a trial server with opendustpan dev

This guide shows how to start a server for a trial or for CI with one command.
`opendustpan dev` takes every decision for you, and says so.
It needs no key, no token, no database and no certificate.
Do not use it in production.
For production, use `opendustpan serve`.

## Start the server

```sh
opendustpan dev
```

The command keeps its data in a directory for each user and listens on `127.0.0.1:8080` with plain HTTP.
It makes the namespace `dev`, so the first stack or workspace needs no other command.
At the first start of a data directory, it also makes the collection `everything` in the namespace `dev`.
It makes two placement rules: one puts each new Pulumi stack in that collection, and one puts each new OpenTofu workspace in it.
The server refuses a new stack or workspace that no placement rule matches, so the rules let `pulumi stack init` and `tofu init` work at once.
The `dev` command makes the rules as the administrator of dev, with the same audit entries as the API.
The server itself makes no rule.
With `--demo`, it makes the same collection and rules in each of `demo-platform` and `demo-data` when it makes that namespace.
A later start makes no rule, also after you delete the rules.
It prints a banner to stderr before the first log line:

```text
opendustpan dev: this server is for a trial or for CI. Do not use it in production.
Data:     /home/alex/.local/share/opendustpan/dev
Server:   http://127.0.0.1:8080
Sign in:  http://localhost:8080/sign-in#code=<code>   (first start on a terminal only, works one time, for 10 minutes)
Token:    in the file /home/alex/.local/share/opendustpan/dev/dev.token
Rule:     in the namespace dev, a rule for each tool puts each new Pulumi stack and each new OpenTofu workspace in the collection everything. List it with: curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8080/api/dustpan/v1/namespaces/<namespace>/placement-rules; delete it with: curl -X DELETE -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8080/api/dustpan/v1/namespaces/<namespace>/placement-rules/<id>
Settings: . /home/alex/.local/share/opendustpan/dev/dev.env
The key-encryption key is in the data directory. A backup of this directory holds the key.
For production, run: opendustpan serve --help
```

The `Rule` line shows at the first start only.
The banner never shows the administrator token.
A signal ends the server: press Ctrl+C.

## Sign in to the console

`dev` serves the web console only when you pass `--console-dir` with a directory that holds a built console.
Without it, the `Sign in` line names the flag.

At the first start of a data directory, on a terminal, the banner prints a sign-in link.
The link holds a code of 32 random bytes after the `#`.
So no request line, access log or `Referer` header holds the code.
The code signs one browser in as the person `dev:admin`.
The bootstrap administrator gives that person the site-administrator role.
The prefix `dev:` and the provider ID `dev` are reserved.
Only the sign-in link and `--demo` (see "A demo person") make such a person.
A server that is not a `dev` server refuses their sessions and tokens.
It works one time, and it stops 10 minutes after the start or when `dev` stops.
Open the link at once.

The link works only from a loopback address or with `--tls`.
The session cookie is `Secure`, and a browser keeps it on plain HTTP only for a loopback name.
So the link names `localhost` and the port of the server when `dev` runs without `--tls`.
If `--listen` is not a loopback address and you set no `--tls`, `dev` makes no link and no code.
The `Sign in` line then says to set `--tls` to sign in from another machine.
In that case, sign in to the console with the token in the token file.

A later start prints no link.
A start with no terminal, for example in a CI job, prints the path of the token file in place of the link.
So a log never holds the code.
To sign in again, sign in to the console with the token in the token file.

## A demo person

Start `dev` with `--demo` to get a person that signs in with a user name and a password:

```sh
opendustpan dev --demo
```

At each start, `dev` sets up the person `dev:demo`.
Its local user name is `demo`.
`dev` makes a new random password, sets it, and prints it one time to the terminal:

```text
Demo user: sign in with the user name demo and the password <password> (new at each start)
```

The password is in no file, no log record and no audit entry.
A second start sets a new password and keeps the one person.
To sign in again, read the line of the new start.

`dev` also makes the namespaces `demo-platform` and `demo-data`, so the data of `make demo` has an owner.
The person has these grants, and no other:

| Role | Object |
| --- | --- |
| `admin` | the namespaces `dev`, `demo-platform` and `demo-data` |
| `viewer` | the installation |

Each grant has the creator `system`.
The audit entry of each change gives the reason "demo user of opendustpan dev".
The person does not hold the site-administrator role.
At each start, `dev` revokes every other grant of the person, for example a role that someone added after the last start.
The audit entry of each revoke names the grant and gives the reason "demo reset".
If the grant is the last site-administrator grant of the installation, the revoke fails and `dev` does not start.
Give the role to a second person first, or start `dev` without `--demo`.
It reads the namespaces and works in them, but it cannot change an installation setting.
The server refuses each change of its password, its user name and its factors with the error code `local-user.demo-fixed`.

Only `dev` takes `--demo`.
`serve` has no flag, variable or key for it.
The seed and the server refuse a database that is not of the kind `dev`.
A server of another kind refuses the sessions and the tokens of the person.

## Use the Pulumi CLI and OpenTofu

`dev` writes the settings of the client tools to the data directory.
Load them in your shell:

```sh
. ~/.local/share/opendustpan/dev/dev.env
```

The file sets the variables that the Pulumi CLI, OpenTofu and the client commands of `opendustpan` read.
Then the tools work with no other setting:

```sh
pulumi login
pulumi stack init dev/demo/dev
pulumi up
```

For OpenTofu, use the `http` backend.
It works over plain HTTP.
Set the address of the workspace in the namespace `dev`:

```hcl
terraform {
  backend "http" {
    address        = "http://127.0.0.1:8080/tofu/state/dev/demo"
    lock_address   = "http://127.0.0.1:8080/tofu/state/dev/demo"
    unlock_address = "http://127.0.0.1:8080/tofu/state/dev/demo"
  }
}
```

```sh
tofu init
tofu apply
```

The `cloud` block of OpenTofu needs HTTPS.
Start `dev` with `--tls`, as the next section shows.

`dev` writes two files for the tools, each with mode 0600:

| File | Content |
| --- | --- |
| `dev.env` | A POSIX `sh` file. It sets `PULUMI_BACKEND_URL`, `DUSTPAN_SERVER`, `TF_CLI_CONFIG_FILE`, `TF_HTTP_USERNAME`, `DUSTPAN_DEV` and the token variables. With `--tls` on Linux, it also sets `SSL_CERT_FILE` and clears `SSL_CERT_DIR`. |
| `dev.tfrc` | The configuration of OpenTofu. It holds a `credentials` block for the host of the server with its port, because no `TF_TOKEN_` variable can name a port. |

In a shell that loads `dev.env`, OpenTofu reads only `dev.tfrc`.
If you have your own configuration of OpenTofu, copy its settings into `dev.tfrc`.

## Use HTTPS

```sh
opendustpan dev --tls
```

`--tls` makes one self-signed certificate for the server.
It makes no certificate authority, and it never changes a trust store of your system or of your user.
The key of the certificate has mode 0600 in the data directory.
The certificate lives 30 days.
A start makes a new one when less than 7 days remain, or when the names change.
The server reads its certificate one time at start, so restart it to use a new one.
The banner prints the date of the end.

The certificate holds these names: `localhost`, `127.0.0.1`, `::1`, the host of `--listen` when it is not a wildcard, the name of the host and each `--hostname`.
Repeat `--hostname` for each name.
A CI job that reaches a service container by its service name sets `--hostname dustpan`.

On Linux, `dev` writes a bundle file.
The bundle holds the root certificates that your shell uses at the start, then the certificate of `dev`.
`dev.env` points `SSL_CERT_FILE` at the bundle and clears `SSL_CERT_DIR`.
So the Pulumi CLI and OpenTofu trust the server, and they keep their roots for the downloads of providers and plugins.

On macOS and on Windows, `dev.env` does not set `SSL_CERT_FILE`, because no file holds the roots of the platform.
The project has not checked the steps that make the certificate trusted on these systems.
Use plain HTTP and the `http` backend there, or trust the certificate with the tools of your system.

## Choose where the data lives

`dev` keeps its data, so a stop never loses the state that tracks real cloud resources.
It chooses the data directory in this order:

1. `--data-dir DIR`: the directory is `DIR`.
2. `--ephemeral`: a new temporary directory.
3. `DUSTPAN_DATA_DIR`: the variable names a parent directory, and the data directory is `dev` in it.
4. None of these: the directory for each user.

The directory for each user is:

| Platform | Directory |
| --- | --- |
| Linux and other Unix | `$XDG_DATA_HOME/opendustpan/dev`, or `~/.local/share/opendustpan/dev` when `XDG_DATA_HOME` is not set |
| macOS | `~/Library/Application Support/opendustpan/dev` |
| Windows | `%LocalAppData%\opendustpan\dev` |

`--ephemeral` and `--data-dir` exclude each other.
With `--ephemeral`, `dev` ignores `DUSTPAN_DATA_DIR` and logs that it does.
It prints a warning in the banner and in the log, at the start and at the stop.
A clean stop deletes the directory, and with it the state that tracks the cloud resources of the trial.
A clean stop leaves no file.
A crash or a kill can leave the directory.

## Run dev in a container

The container image sets `DUSTPAN_DATA_DIR=/data`, so `dev` keeps its data in the directory `dev` of the volume `/data`.
The default command of the image is `serve`, which stops without a key, so give the command `dev`.
A container needs a listen address that the port mapping can reach, so pass `--listen 0.0.0.0:8080`.
Map the port to the loopback address of the host, so that only your machine can reach the server:

```sh
docker run -p 127.0.0.1:8080:8080 opendustpan:dev dev --listen 0.0.0.0:8080
```

Add `-v ./trial:/data` to keep the data after the container stops.
The container user must be able to write to that directory.

`dev` makes the data directory with mode 0700.
At each start, `dev` refuses to start when the group or others can access the directory, or when another user owns it.
It does the same for the key file, the token file, the token file of `--admin-token-file` and the files of the certificate.
The error names the path and the one command that helps.
`dev` checks the owner and the mode on Linux and on the other Unix systems.
It does not check them on Windows yet.

The directory holds:

| File | Content |
| --- | --- |
| `dev.db` and `blobs` | The data of the server. |
| `dev.key` | The key-encryption key, after a line that marks it as a key of `dev`. |
| `dev.token` | The administrator token that `dev` made. |
| `dev.crt`, `dev.tls.key` and `dev.ca-bundle.crt` | The certificate, its key and the bundle, with `--tls`. |
| `dev.env` and `dev.tfrc` | The settings of the client tools. |
| `dev.addr` | The address, the process ID and the start time of the running server. `dev` deletes it at the stop. |

The key-encryption key is in the data directory, so a backup of the directory holds the key.
`dev` has no backup command and no backup target.

## Use dev in CI

Start `dev` with a data directory in the temporary space of the job and a token of your choice:

```sh
export DUSTPAN_DEV_ADMIN_TOKEN="$(openssl rand -hex 32)"
opendustpan dev --data-dir "$RUNNER_TEMP/dustpan" --listen 127.0.0.1:8080 &
```

`dev` reads only flags.
It reads two variables: `DUSTPAN_DATA_DIR` and `DUSTPAN_DEV_ADMIN_TOKEN`.
It never reads `DUSTPAN_ADMIN_TOKEN` or `DUSTPAN_ADMIN_TOKEN_FILE`, because a shell or a job often exports the token of production in them.
At the start, `dev` clears each variable of a secret setting of `serve` from its environment, and clears `DUSTPAN_DEV_ADMIN_TOKEN` after it reads it.

To choose the token, set `DUSTPAN_DEV_ADMIN_TOKEN` or pass `--admin-token-file FILE`.
With the variable, `dev.env` holds no token: it reads the variable in the shell that loads it.
`dev.tfrc` still holds the token.
With the file, `dev.env` sets `DUSTPAN_ADMIN_TOKEN_FILE` to its path and holds no token.
Set `PULUMI_ACCESS_TOKEN` and `TF_HTTP_PASSWORD` to the token, because the Pulumi CLI and OpenTofu read no token file.

Wait until the server is ready before you start the tools:

```sh
until curl -fsS http://127.0.0.1:8080/health/ready; do sleep 1; done
. "$RUNNER_TEMP/dustpan/dev.env"
```

A job can test the size limits of production.
It passes `--max-checkpoint-bytes`, `--max-secret-batch-bytes`, `--max-state-bytes`, `--max-body-bytes-in-flight` or `--blob-cache-bytes`.
A job on a reused runner that sets no `--data-dir` and no `--ephemeral` leaves files in the directory for each user.

## Reach the server from another host

`dev` binds the loopback address by default.
`--listen` accepts another address.
For an address that is not a loopback address, the banner and the log name the address at each start.
Without `--tls`, they also say that the administrator token crosses the network in plain text.
Use `--tls` for any address that other hosts can reach.

## What dev does not take

`dev` takes no `--config`, no database, no blob store, no backup target, no key setting, no audit sink, no OpenID Connect provider and no proxy setting.
It runs the server code of `serve` with its own defaults.
To use any of these settings, run `serve`.

## Installation kind

Each installation has a kind: `dev` or `production`.
The first command that starts on a new database claims the kind, and no command or API changes it later.
`dev` claims `dev`, and `serve` claims `production`.
Each command refuses a database of the other kind, before it changes the schema:

```text
opendustpan: opendustpan dev made this database: serve refuses it. Set --database to a new database.
opendustpan: opendustpan serve made this database: dev refuses it. Set a new --data-dir.
```

`dev` also refuses a directory that holds files but no key file of `dev`.
So `dev --data-dir` on the directory of a production database never opens, changes or claims that database.

An authorized caller reads the kind in the capabilities document:

```sh
curl -H "Authorization: Bearer $DUSTPAN_ADMIN_TOKEN" http://127.0.0.1:8080/api/dustpan/v1/capabilities
```

```json
{"apiVersion":"v1","installationKind":"dev"}
```

A caller with no sign-in gets `apiVersion` only.
The audit entry of each start records the kind.

A backup records the kind of its installation.
A production server refuses to restore a backup of a dev installation, unless you start it with `--unsafe-allow-restoring-dev-installation-backups-into-production`.
Also then, the restore deletes the person `dev:admin` and all other data of dev people.
The [backup guide](backup-restore-upgrade.md#restore-a-backup-of-a-dev-installation) shows the steps.

## Start again after a schema change

Until the first release, a new build of OpenDustpan can change the database schema.
`dev` then refuses the database of an earlier build and names the command that helps:

```text
opendustpan: the schema changed: run opendustpan dev reset to start again. That command moves the old data aside as dev.db.reset.bak in the data directory: ...
```

`dev` never resets a database on its own. To start again on an empty database, stop `dev` and run:

```sh
opendustpan dev reset
```

Add `--data-dir` when `dev` runs with `--data-dir`.
The command does these steps:

1. It refuses the directory when it is not a data directory of `dev`, or when it is a symbolic link. It also refuses it when a `dev` server or another process holds the database.
2. It refuses a database that is not a regular file or that has a second name (a hard link). It refuses a `blobs` directory that is a symbolic link, that another user owns, or that the group or others can access.
3. It reads the installation kind of the database. It refuses a database of `serve`, a database with no kind and a database with a kind that it does not know. It then changes nothing.
4. It refuses the directory when it holds a copy with the suffix `.old`, such as `dev.db.reset.bak.old`. It names each copy and changes nothing. See [Keep a copy of a reset](#keep-a-copy-of-a-reset).
5. It moves `dev.db`, its log files and the `blobs` directory aside, as `dev.db.reset.bak` and `blobs.reset.bak`. It keeps the copies of the reset before until the new `dev.db` exists. Then it deletes them, so the directory keeps the data of one reset only.
6. It writes a log line that names the copies, makes the schema of the binary on a new `dev.db`, and claims the kind `dev`.
7. It writes an audit entry that names the copies.

The key file, the token and the certificate stay. The next `dev` starts on the new database.
To look at the old data, open `dev.db.reset.bak` with an earlier build.

### Keep a copy of a reset

A reset that does not finish can leave a copy with the suffix `.old` in the data directory. The copy can hold the last good backup. `dev reset` refuses the directory while the copy is there, and the error names each copy.

To keep the copy and run the reset again:

1. Stop `dev`.
2. Move each named copy out of the data directory. Keep it. Do not delete it.
3. Run `opendustpan dev reset` again.

If the reset stops after it moved the data aside, the error names the copies it kept and the paths it moved. Use the same steps.

### Put a backup back

To put back the data of the last reset, use the copies that the reset kept:

1. Stop `dev`.
2. Move the current `dev.db` and the `blobs` directory to another directory. Keep them.
3. Rename the copies back:

```sh
mv dev.db.reset.bak dev.db
mv blobs.reset.bak blobs
```

If the files `dev.db.reset.bak-wal` and `dev.db.reset.bak-shm` exist, rename them to `dev.db-wal` and `dev.db-shm` the same way. The next `dev` starts on the restored database.

`serve` never offers a reset. It refuses a database with another schema. It says to restore a backup of the same build, or to set `--database` to a new database.

## Keep the stacks and workspaces of a trial

No command and no flag turns a dev installation into a production installation.
To keep a stack or a workspace, move it to a production server with the commands of its tool.
The stacks and workspaces keep the `dev/` prefix of their names.

For a Pulumi stack, export it and import it into the new server:

```sh
pulumi stack export --show-secrets --file stack.json
pulumi stack import --file stack.json
```

The secret values of the configuration of the stack use the key of `dev`.
Set each one again on the new stack.
Stack history, tags and update events do not move.

For an OpenTofu workspace, pull the state, change the backend or the `cloud` block, and push the state:

```sh
tofu state pull > state.json
# Change the backend or the cloud block to the new server.
tofu init -reconfigure
tofu state push state.json
```

The exported files hold secret values in plain text.
Delete them after the import.
